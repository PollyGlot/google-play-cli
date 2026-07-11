package history

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// --- test doubles ----------------------------------------------------------

// utf16LE encodes s as UTF-16LE with a BOM — the byte shape of a real report.
func utf16LE(s string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, u := range utf16.Encode([]rune(s)) {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], u)
		buf.Write(b[:])
	}
	return buf.Bytes()
}

const reportCSV = "Package Name,Star Rating,Reviewer Language,Review Title,Review Text,App Version Name,App Version Code\n" +
	"com.example.app,5,fr,Génial,\"Très bonne app 🎉\",4.2.0,42\n" +
	"com.example.app,1,en,Meh,Crashes,4.2.0,42\n"

// historyRT serves the /token exchange, a GCS list, and a GCS media fetch. It
// records every non-token request path so tests can assert the wire shape.
type historyRT struct {
	mu       sync.Mutex
	calls    []string
	listBody string
	getBody  []byte
	code     int // forced non-2xx for the GCS call (0 -> 200)
	errBody  string
}

func (r *historyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return resp(200, []byte(`{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`)), nil
	}
	r.mu.Lock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Host+req.URL.EscapedPath()+"?"+req.URL.RawQuery)
	r.mu.Unlock()

	if r.code != 0 {
		return resp(r.code, []byte(r.errBody)), nil
	}
	if strings.Contains(req.URL.Path, "/o/") { // media fetch
		return resp(200, r.getBody), nil
	}
	return resp(200, []byte(r.listBody)), nil // list
}

func resp(code int, body []byte) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// newRC wires a RunContext whose HTTP client routes through rt (wrapped in a
// ScopeObserver so the token-exchange scope can be asserted), with the storage
// scope declared and a developer-id resolved so the bucket derives.
func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *transport.ScopeObserver, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	observed, obs := transport.WithScopeObserver(rt)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: observed})
	var stdout, stderr bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout, Stderr: &stderr}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON, Scope: token.StorageReadOnlyScope})
	rc.Account = sa
	rc.Resolved = &config.Resolved{DeveloperID: "123456"}
	return rc, obs, &stdout, &stderr
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

// --- tests -----------------------------------------------------------------

// TestRun_monthFetch_wirePathAndScope covers the primary acceptance criterion:
// `--month 2026-06` fetches reviews/reviews_P_202606.csv from the derived
// bucket, and the token exchange carries the devstorage.read_only scope.
func TestRun_monthFetch_wirePathAndScope(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, obs, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(r.(Payload).Rows); got != 2 {
		t.Fatalf("want 2 parsed rows, got %d", got)
	}

	// Wire path: media fetch of the derived bucket + month object.
	found := false
	for _, c := range rt.calls {
		if strings.Contains(c, "storage.googleapis.com/storage/v1/b/pubsite_prod_rev_123456/o/reviews%2Freviews_com.example.app_202606.csv") &&
			strings.Contains(c, "alt=media") {
			found = true
		}
	}
	if !found {
		t.Errorf("did not fetch the derived bucket/month object; calls=%v", rt.calls)
	}

	// The token request carried the devstorage.read_only scope (least privilege).
	scopes := obs.Scopes()
	if !containsScope(scopes, token.StorageReadOnlyScope) {
		t.Errorf("token exchange scopes = %v, want to include %q", scopes, token.StorageReadOnlyScope)
	}
	if containsScope(scopes, token.AndroidPublisherScope) {
		t.Errorf("token exchange must NOT carry the androidpublisher scope; got %v", scopes)
	}
}

func containsScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// TestRun_noMonth_selectsLatest: omitting --month lists the bucket and fetches
// the newest month present.
func TestRun_noMonth_selectsLatest(t *testing.T) {
	rt := &historyRT{
		listBody: `{"items":[
			{"name":"reviews/reviews_com.example.app_202604.csv"},
			{"name":"reviews/reviews_com.example.app_202606.csv"},
			{"name":"reviews/reviews_com.example.app_202605.csv"}
		]}`,
		getBody: utf16LE(reportCSV),
	}
	rc, _, _, _ := newRC(t, rt)

	if _, err := Run(rc, Input{Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var listed, fetched bool
	for _, c := range rt.calls {
		if strings.Contains(c, "/o?") && strings.Contains(c, "prefix=reviews") {
			listed = true
		}
		if strings.Contains(c, "reviews%2Freviews_com.example.app_202606.csv") {
			fetched = true // the LATEST of the three
		}
	}
	if !listed {
		t.Errorf("no --month should list the bucket; calls=%v", rt.calls)
	}
	if !fetched {
		t.Errorf("no --month should fetch the latest (202606); calls=%v", rt.calls)
	}
}

func TestRun_noReports_exit30(t *testing.T) {
	rt := &historyRT{listBody: `{"items":[]}`}
	rc, _, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("empty bucket should be exit 30, got %d (err: %v)", code, err)
	}
}

// TestRun_bucketOverride: --bucket wins over the derived name (and no
// developer-id is required).
func TestRun_bucketOverride(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, _, _, _ := newRC(t, rt)
	rc.Resolved = &config.Resolved{} // no developer-id at all

	if _, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06", Bucket: "custom-bucket-name"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range rt.calls {
		if strings.Contains(c, "/b/custom-bucket-name/o/") {
			found = true
		}
	}
	if !found {
		t.Errorf("--bucket override not used; calls=%v", rt.calls)
	}
}

func TestRun_forbidden_exit11_namesConsoleAndPermission(t *testing.T) {
	rt := &historyRT{code: 403, errBody: `{"error":{"code":403,"message":"forbidden"}}`}
	rc, _, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("403 should map to exit 11, got %d", code)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Copy Cloud Storage URI") {
		t.Errorf("403 error should name the Console \"Copy Cloud Storage URI\" button; got %q", msg)
	}
	if !strings.Contains(msg, "View app information") {
		t.Errorf("403 error should name the \"View app information\" permission; got %q", msg)
	}
}

func TestRun_notFound_exit30_namesConsoleAndPermission(t *testing.T) {
	rt := &historyRT{code: 404, errBody: `{"error":{"code":404,"message":"no such bucket"}}`}
	rc, _, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("404 should map to exit 30, got %d", code)
	}
	if !strings.Contains(err.Error(), "Copy Cloud Storage URI") || !strings.Contains(err.Error(), "View app information") {
		t.Errorf("404 error should name the Console button + permission; got %q", err.Error())
	}
}

func TestRun_badMonth_exit2_noNetwork(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, _, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app", Month: "2026-13"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Fatalf("bad --month should be exit 2, got %d", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("bad --month reached the network; calls=%v", rt.calls)
	}
}

func TestRun_noDeveloperId_exit10(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, _, _, _ := newRC(t, rt)
	rc.Resolved = &config.Resolved{} // no developer-id, no --bucket

	_, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Fatalf("unresolved developer-id should be exit 10, got %d (err: %v)", code, err)
	}
}

func TestPayload_JSON_emitsParsedRows(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, _, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("Render JSON: %v", err)
	}
	var env struct {
		Reviews []struct {
			PackageName      string `json:"packageName"`
			StarRating       string `json:"starRating"`
			ReviewerLanguage string `json:"reviewerLanguage"`
			ReviewText       string `json:"reviewText"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output not a {\"reviews\":[...]} envelope: %v\n%s", err, buf.String())
	}
	if len(env.Reviews) != 2 {
		t.Fatalf("want 2 rows, got %d", len(env.Reviews))
	}
	if env.Reviews[0].StarRating != "5" || env.Reviews[0].ReviewerLanguage != "fr" {
		t.Errorf("row 0 fields wrong: %+v", env.Reviews[0])
	}
	if env.Reviews[0].ReviewText != "Très bonne app 🎉" {
		t.Errorf("non-ASCII review text not passed through: %q", env.Reviews[0].ReviewText)
	}
}

func TestPayload_Table_defaultColumns(t *testing.T) {
	rt := &historyRT{getBody: utf16LE(reportCSV)}
	rc, _, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Month: "2026-06"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("Render table: %v", err)
	}
	out := buf.String()
	for _, h := range []string{"DATE", "STARS", "LOCALE", "VERSION", "TITLE", "SUMMARY"} {
		if !strings.Contains(out, h) {
			t.Errorf("table header missing %q in:\n%s", h, out)
		}
	}
	// device/reply are non-default columns.
	if strings.Contains(out, "DEVICE") || strings.Contains(out, "REPLY") {
		t.Errorf("default table should not include device/reply:\n%s", out)
	}
	if !strings.Contains(out, "Génial") || !strings.Contains(out, "4.2.0 (42)") {
		t.Errorf("table missing expected cells:\n%s", out)
	}
}
