package pull_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	pullcmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// subsRT terminates the /token exchange and serves subscriptions.list pages,
// recording every API call. status/body override the happy path for refusal
// tests.
type subsRT struct {
	mu     sync.Mutex
	calls  []string
	urls   []string
	status int
	body   string
}

func (r *subsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.urls = append(r.urls, req.URL.String())
	if r.status != 0 {
		return jsonResp(r.status, r.body), nil
	}
	if strings.Contains(req.URL.RawQuery, "pageToken=p2") {
		return jsonResp(200, `{"subscriptions":[{"productId":"pro","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Pro"}]}]}`), nil
	}
	return jsonResp(200, `{"subscriptions":[{"productId":"premium","packageName":"com.example.app","listings":[{"languageCode":"en-US","title":"Premium"}]}],"nextPageToken":"p2"}`), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

// TestRun_mirrorsLiveCatalogToDir asserts pull follows pagination to
// completion, writes one stripped file per subscription, removes stale catalog
// files, and passes the merged envelope through as json — no Edit anywhere.
func TestRun_mirrorsLiveCatalogToDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stale.json"), []byte(`{"productId":"stale"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := &subsRT{}
	rc := newRC(t, rt)
	r, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, u := range rt.urls {
		if strings.Contains(u, "/edits/") {
			t.Errorf("url %q must not open an Edit", u)
		}
	}
	if got := len(rt.urls); got != 2 {
		t.Fatalf("want 2 list pages, got %d (%v)", got, rt.urls)
	}
	b, err := os.ReadFile(filepath.Join(dir, "premium.json"))
	if err != nil {
		t.Fatalf("premium.json not written: %v", err)
	}
	if strings.Contains(string(b), "packageName") {
		t.Errorf("catalog file %s must strip packageName", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "pro.json")); err != nil {
		t.Errorf("pro.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale.json should have been removed, stat err = %v", err)
	}
	// ADR-0003: json is the merged ListSubscriptionsResponse envelope.
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var envelope struct {
		Subscriptions []map[string]any `json:"subscriptions"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json output %s: %v", out.String(), err)
	}
	if len(envelope.Subscriptions) != 2 {
		t.Errorf("merged envelope has %d subscriptions, want 2", len(envelope.Subscriptions))
	}
}

// TestRun_humanSummary asserts the table view names the files written.
func TestRun_humanSummary(t *testing.T) {
	dir := t.TempDir()
	rt := &subsRT{}
	rc := newRC(t, rt)
	r, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := out.String()
	for _, want := range []string{"pulled 2 subscription(s)", "premium.json", "pro.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing %q", got, want)
		}
	}
}

// TestRun_emptyLiveNonEmptyLocal_refuses asserts an unexpectedly-empty live
// listing never erases a populated catalog directory (mis-set --package /
// scope loss): pull refuses (exit 2) and the files survive.
func TestRun_emptyLiveNonEmptyLocal_refuses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "premium.json"), []byte(`{"productId":"premium"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := &subsRT{status: 200, body: `{}`}
	rc := newRC(t, rt)
	_, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: dir})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "refusing to erase") {
		t.Errorf("refusal %q should explain the erase risk", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "premium.json")); statErr != nil {
		t.Errorf("premium.json must survive the refusal: %v", statErr)
	}
}

// TestRun_missingPackage_exit2_noNetwork asserts no package (flag or pin) is
// CLI misuse before any HTTP.
func TestRun_missingPackage_exit2_noNetwork(t *testing.T) {
	rt := &subsRT{}
	rc := newRC(t, rt)
	_, err := pullcmd.Run(rc, pullcmd.Input{Dir: t.TempDir()})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_403_hintsAtMonetizationAccess asserts a forbidden list maps to exit
// 11 with the monetization-access hint.
func TestRun_403_hintsAtMonetizationAccess(t *testing.T) {
	rt := &subsRT{status: 403, body: `{"error":{"message":"The caller does not have permission"}}`}
	rc := newRC(t, rt)
	_, err := pullcmd.Run(rc, pullcmd.Input{Package: "com.example.app", Dir: t.TempDir()})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "monetization") {
		t.Errorf("403 refusal %q should hint at monetization access", err.Error())
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
