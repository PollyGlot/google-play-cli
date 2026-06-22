package view

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
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// getRT covers the OAuth2 /token exchange and the single reviews.get GET a
// `reviews view` invocation makes. code/errBody force a non-2xx for the
// error-mapping tests (0 → 200 with body). It records the path it saw so a test
// can assert the wire call.
type getRT struct {
	t       *testing.T
	body    string
	code    int
	errBody string

	mu        sync.Mutex
	gotPath   string
	tokenHits int
	getCalls  int
}

func (r *getRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	// reviews.get is GET .../reviews/<id> — not the collection, not :reply.
	if req.Method != http.MethodGet || !strings.Contains(req.URL.Path, "/reviews/") {
		r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		return nil, nil
	}
	r.getCalls++
	r.gotPath = req.URL.Path
	if r.code != 0 {
		return jsonResp(r.code, r.errBody), nil
	}
	return jsonResp(200, r.body), nil
}

func jsonResp(code int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: h}
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

// newRC wires a RunContext whose HTTP client routes through rt, in the given
// output Format, and returns the captured stdout and stderr buffers.
func newRC(t *testing.T, rt http.RoundTripper, format output.Format) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout, Stderr: &stderr}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: format})
	rc.Account = sa
	return rc, &stdout, &stderr
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

// oneReviewBody is a single reviews.get response with a user comment and one
// developer reply, reused across the rendering tests.
const oneReviewBody = `{
	"reviewId":"gp:AOqpT123",
	"authorName":"Jane Doe",
	"comments":[
		{"userComment":{"text":"Crashes on launch\nplease fix","starRating":2,"reviewerLanguage":"en-US","device":"flame","appVersionName":"1.2.3","appVersionCode":45,"lastModified":{"seconds":"1700000000"}}},
		{"developerComment":{"text":"Sorry — fixed in 1.2.4","lastModified":{"seconds":"1700000600"}}}
	]
}`

func TestRun_human_headerAndThread(t *testing.T) {
	rt := &getRT{t: t, body: oneReviewBody}
	rc, _, _ := newRC(t, rt, output.FormatTable)

	r, err := Run(rc, Input{Package: "com.example.app", ReviewID: "gp:AOqpT123"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The wire call lands on the single-review path, reviewId colon literal.
	wantPath := "/androidpublisher/v3/applications/com.example.app/reviews/gp:AOqpT123"
	if rt.gotPath != wantPath {
		t.Errorf("path = %q, want %q", rt.gotPath, wantPath)
	}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("Render table: %v", err)
	}
	out := buf.String()

	for _, h := range []string{"AUTHOR", "STARS", "DATE", "LOCALE", "DEVICE", "APP_VERSION", "REVIEW_ID"} {
		if !strings.Contains(out, h) {
			t.Errorf("table header missing %q in:\n%s", h, out)
		}
	}
	for _, v := range []string{"Jane Doe", "en-US", "flame", "1.2.3 (45)", "gp:AOqpT123"} {
		if !strings.Contains(out, v) {
			t.Errorf("table missing value %q in:\n%s", v, out)
		}
	}
	// The thread shows the full review body (not a one-line summary) and the
	// developer reply with its date.
	if !strings.Contains(out, "Crashes on launch") || !strings.Contains(out, "please fix") {
		t.Errorf("table should show the full review body in:\n%s", out)
	}
	if !strings.Contains(out, "Developer reply") || !strings.Contains(out, "Sorry — fixed in 1.2.4") {
		t.Errorf("table should show the developer reply in:\n%s", out)
	}
}

func TestRun_json_passthroughVerbatim(t *testing.T) {
	rt := &getRT{t: t, body: oneReviewBody}
	rc, _, _ := newRC(t, rt, output.FormatJSON)

	r, err := Run(rc, Input{Package: "com.example.app", ReviewID: "gp:AOqpT123"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("Render json: %v", err)
	}
	// --output json is the Review object verbatim (ADR-0003) — same reviewId,
	// and the developerComment survives the pass-through.
	var got struct {
		ReviewID string `json:"reviewId"`
		Comments []struct {
			DeveloperComment *struct {
				Text string `json:"text"`
			} `json:"developerComment"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the verbatim Review JSON: %v\n%s", err, buf.String())
	}
	if got.ReviewID != "gp:AOqpT123" {
		t.Errorf("json reviewId = %q, want gp:AOqpT123", got.ReviewID)
	}
	if len(got.Comments) != 2 || got.Comments[1].DeveloperComment == nil {
		t.Errorf("json passthrough should preserve the developer reply; got %s", buf.String())
	}
}

func TestRun_markdown_recordAndBlockquotes(t *testing.T) {
	rt := &getRT{t: t, body: oneReviewBody}
	rc, _, _ := newRC(t, rt, output.FormatMarkdown)

	r, err := Run(rc, Input{Package: "com.example.app", ReviewID: "gp:AOqpT123"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatMarkdown, r.Renderers()); err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Review by Jane Doe") {
		t.Errorf("markdown should lead with a heading naming the author:\n%s", out)
	}
	if !strings.Contains(out, "- **Review id**: gp:AOqpT123") {
		t.Errorf("markdown should list the reviewId:\n%s", out)
	}
	if !strings.Contains(out, "> Crashes on launch") {
		t.Errorf("markdown should quote the review body:\n%s", out)
	}
	if !strings.Contains(out, "Developer reply") || !strings.Contains(out, "> Sorry") {
		t.Errorf("markdown should quote the developer reply:\n%s", out)
	}
}

func TestRun_notFound_exit30_namesWindow(t *testing.T) {
	rt := &getRT{t: t, code: 404, errBody: `{"error":{"code":404,"message":"not found"}}`}
	rc, _, _ := newRC(t, rt, output.FormatJSON)

	_, err := Run(rc, Input{Package: "com.example.app", ReviewID: "gp:gone"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("404 should map to exit 30, got %d (err: %v)", code, err)
	}
	// The message must name the 7-day window so a valid-but-expired id is
	// understood, and must name the review (not point at `gplay apps list`).
	if !strings.Contains(err.Error(), "7 days") {
		t.Errorf("404 message should name the 7-day window; got %q", err.Error())
	}
	if strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("404 hint must not borrow list's package guidance; got %q", err.Error())
	}
}

func TestRun_forbidden_exit11_withGrantHint(t *testing.T) {
	rt := &getRT{t: t, code: 403, errBody: `{"error":{"code":403,"message":"forbidden"}}`}
	rc, _, _ := newRC(t, rt, output.FormatJSON)

	_, err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("403 should map to exit 11, got %d (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "Reply to reviews") {
		t.Errorf("403 should carry the shared 'Reply to reviews' hint; got %q", err.Error())
	}
}

func TestRun_emptyReviewID_exit2_noNetwork(t *testing.T) {
	rt := &getRT{t: t, body: oneReviewBody}
	rc, _, _ := newRC(t, rt, output.FormatJSON)

	_, err := Run(rc, Input{Package: "com.example.app", ReviewID: "   "})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("blank reviewId: exit = %d, want 2", code)
	}
	if rt.tokenHits != 0 || rt.getCalls != 0 {
		t.Errorf("a blank reviewId must fail before the network; tokenHits=%d getCalls=%d", rt.tokenHits, rt.getCalls)
	}
}

func TestRun_noPackage_exit2_noNetwork(t *testing.T) {
	rt := &getRT{t: t, body: oneReviewBody}
	rc, _, _ := newRC(t, rt, output.FormatJSON)

	_, err := Run(rc, Input{ReviewID: "r1"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("no package: exit = %d, want 2", code)
	}
	if rt.tokenHits != 0 || rt.getCalls != 0 {
		t.Errorf("an unresolved package must fail before the network; tokenHits=%d getCalls=%d", rt.tokenHits, rt.getCalls)
	}
}
