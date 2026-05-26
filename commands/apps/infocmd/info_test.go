// Package infocmd_test exercises `gplay apps info` at the kernel level:
// a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// releases/list harness — the transport FAILS on any PUT or :commit,
// because `apps info` is a read-only listing (open Edit → details.get
// → listings.get → discard, never commit).
package infocmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/apps/infocmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// infoRT terminates the OAuth2 /token exchange and routes the apps-info
// sequence: edits.insert, edits.details.get, edits.listings.get(lang),
// edits.delete. It deliberately has NO PUT or :commit branch: reaching
// one means the command tried to mutate state, which a read-only
// listing must never do — so the transport fails the test.
type infoRT struct {
	t       *testing.T
	editID  string
	details string
	listing string

	detailsCode int
	listingCode int

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *infoRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
		code := r.detailsCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.details), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/listings/"):
		code := r.listingCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.listing), nil
	}
	r.t.Fatalf("unexpected request (apps info is read-only): %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
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

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{}
	return rc, &stdout
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

// TestRun_happyPath asserts the read-only vertical slice: /token first,
// then edits.insert, details.get, listings.get(defaultLanguage),
// edits.delete. The returned payload carries the three Details fields,
// and --output json emits the {"details":..,"listing":..} envelope
// verbatim (explicit exception to ADR-0003).
func TestRun_happyPath(t *testing.T) {
	detailsBody := `{"contactEmail":"hi@example.com","contactPhone":"+1","contactWebsite":"https://x","defaultLanguage":"en-US"}`
	listingBody := `{"language":"en-US","title":"MyApp","shortDescription":"hi","fullDescription":"world","video":""}`
	rt := &infoRT{
		t:       t,
		editID:  "edit-info",
		details: detailsBody,
		listing: listingBody,
	}
	rc, _ := newRC(t, rt)

	r, err := infocmd.Run(rc, infocmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-info/details",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-info/listings/en-US",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-info",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// JSON pass-through: the envelope wraps both upstream bodies verbatim.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var env struct {
		Details json.RawMessage `json:"details"`
		Listing json.RawMessage `json:"listing"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &env); err != nil {
		t.Fatalf("JSON output is not the {details,listing} envelope: %v\nout=%s", err, jsonOut.String())
	}
	if strings.TrimSpace(string(env.Details)) != strings.TrimSpace(detailsBody) {
		t.Errorf("envelope.details = %s\nwant %s", env.Details, detailsBody)
	}
	if strings.TrimSpace(string(env.Listing)) != strings.TrimSpace(listingBody) {
		t.Errorf("envelope.listing = %s\nwant %s", env.Listing, listingBody)
	}
}

// TestRun_usesPin_whenNoFlag asserts that without --package the command
// falls back to rc.Resolved.Pin (the .gplay/config.json pin), matching
// the same precedence rule as `gplay releases list`.
func TestRun_usesPin_whenNoFlag(t *testing.T) {
	rt := &infoRT{
		t:       t,
		editID:  "edit-pin",
		details: `{"defaultLanguage":"fr-FR","contactEmail":"hi@example.com"}`,
		listing: `{"language":"fr-FR","title":"MonApp"}`,
	}
	rc, _ := newRC(t, rt)
	rc.Resolved.Pin = "com.pinned.app"

	if _, err := infocmd.Run(rc, infocmd.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range rt.calls {
		if strings.Contains(c, "com.pinned.app") {
			return
		}
	}
	t.Errorf("expected calls scoped to com.pinned.app, got: %v", rt.calls)
}

// TestRun_missingPackage_exit2 asserts that with neither --package nor
// a pinned project, the command short-circuits with a usage error
// before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &infoRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := infocmd.Run(rc, infocmd.Input{})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_noAccount_exit10 asserts that with no resolved Account the
// command fails auth (exit 10) before any HTTP call — there is no
// dry-run path for a read-only listing.
func TestRun_noAccount_exit10(t *testing.T) {
	rt := &infoRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := infocmd.Run(rc, infocmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}

// TestRun_detailsGet403_exit11 asserts a 403 on details.get bubbles up
// as exit 11 (authorization). The Edit is still discarded.
func TestRun_detailsGet403_exit11(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-403",
		detailsCode: 403,
		details:     `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := infocmd.Run(rc, infocmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
}

// TestRun_detailsGet404_exit30 asserts a 404 on details.get maps to
// exit 30 (API 4xx other than auth/perms).
func TestRun_detailsGet404_exit30(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-404",
		detailsCode: 404,
		details:     `{"error":{"code":404,"message":"app not found"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := infocmd.Run(rc, infocmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
}

// TestRenderTable_showsAllThreeFields asserts the table view surfaces
// defaultLanguage, title, contactEmail — the three fields documented
// in the AC. The exact layout is up to the renderer; the contract is
// that each value appears somewhere in the output.
func TestRenderTable_showsAllThreeFields(t *testing.T) {
	p := infocmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		Title:           "MyApp",
		ContactEmail:    "hi@example.com",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "MyApp", "hi@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderMarkdown_showsAllThreeFields asserts the markdown view
// surfaces the same three fields. Per docs/DESIGN.md §7, markdown for a
// single-record info command is a `- **Field**: value` list rather than
// a GFM table.
func TestRenderMarkdown_showsAllThreeFields(t *testing.T) {
	p := infocmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		Title:           "MyApp",
		ContactEmail:    "hi@example.com",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "MyApp", "hi@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}
