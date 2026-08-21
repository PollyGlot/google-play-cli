// Package detailscmd_test exercises `gplay apps details view` (the record
// read) at the kernel level: a RunContext built by hand, a RoundTripper
// injected via the oauth2.HTTPClient context key, and Run invoked
// directly. Mirrors the apps-view harness: the transport FAILS on any
// PUT/PATCH/:commit AND on listings.get, because reading App details is a
// read-only, single-endpoint operation (open Edit → details.get →
// discard, never commit, never a second endpoint).
package detailscmd_test

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

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// readRT terminates the OAuth2 /token exchange and routes the
// apps-details read sequence: edits.insert, edits.details.get,
// edits.delete. It has NO PUT/PATCH/:commit branch and NO listings.get
// branch: reaching either means the command tried to mutate state or hit
// a second endpoint, which a single-endpoint read-only command must
// never do, so the transport fails the test.
type readRT struct {
	t       *testing.T
	editID  string
	details string

	detailsCode int

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *readRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	}
	r.t.Fatalf("unexpected request (apps details is read-only, single-endpoint): %s %s", req.Method, req.URL)
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

// TestRun_happyPath asserts the read-only single-endpoint vertical slice:
// /token first, then edits.insert, details.get, edits.delete (no
// listings.get, no commit). The returned payload carries the four App
// details fields, and --output json emits the details.get body verbatim
// (clean ADR-0003 pass-through: no envelope).
func TestRun_happyPath(t *testing.T) {
	body := `{"contactEmail":"hi@example.com","contactPhone":"+1 555 0100","contactWebsite":"https://x.example","defaultLanguage":"en-US"}`
	rt := &readRT{t: t, editID: "edit-details", details: body}
	rc, _ := newRC(t, rt)

	r, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-details/details",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-details",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// JSON pass-through: the details.get body verbatim, no gplay envelope.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(body) {
		t.Errorf("JSON output = %s\nwant details.get body verbatim = %s", got, body)
	}
}

// TestRun_usesPin_whenNoFlag asserts that without --package the command
// falls back to rc.Resolved.Pin (the .gplay/config.json pin), matching
// the same precedence rule as `gplay apps view` / `tracks view`.
func TestRun_usesPin_whenNoFlag(t *testing.T) {
	rt := &readRT{
		t:       t,
		editID:  "edit-pin",
		details: `{"defaultLanguage":"fr-FR","contactEmail":"bonjour@example.fr"}`,
	}
	rc, _ := newRC(t, rt)
	rc.Resolved.Pin = "com.pinned.app"

	if _, err := detailscmd.Run(rc, detailscmd.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range rt.calls {
		if strings.Contains(c, "com.pinned.app") {
			return
		}
	}
	t.Errorf("expected calls scoped to com.pinned.app, got: %v", rt.calls)
}

// TestRun_missingPackage_exit2 asserts that with neither --package nor a
// pinned project, the command short-circuits with a usage error before
// any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &readRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := detailscmd.Run(rc, detailscmd.Input{})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_noAccount_exit10 asserts that with no resolved Account the
// command fails auth (exit 10) before any HTTP call: there is no
// dry-run path for a read.
func TestRun_noAccount_exit10(t *testing.T) {
	rt := &readRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}

// TestRun_get403_exit11_apiAccessHint asserts a 403 on details.get
// bubbles up as exit 11 (authorization) with an actionable hint pointing
// at the Play Console API access page.
func TestRun_get403_exit11_apiAccessHint(t *testing.T) {
	rt := &readRT{
		t:           t,
		editID:      "edit-403",
		detailsCode: 403,
		details:     `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("403 error message = %q, want it to mention 'API access'", err.Error())
	}
}

// TestRun_get404_exit30_appsListHint asserts a 404 on details.get maps to
// exit 30 (API 4xx other than auth/perms) with a hint pointing at
// `gplay apps list`.
func TestRun_get404_exit30_appsListHint(t *testing.T) {
	rt := &readRT{
		t:           t,
		editID:      "edit-404",
		detailsCode: 404,
		details:     `{"error":{"code":404,"message":"app not found"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("404 error message = %q, want it to mention 'gplay apps list'", err.Error())
	}
}

// TestRenderTable_showsAllFourFields asserts the table view surfaces all
// four App details fields. The exact layout is up to the renderer; the
// contract is that each value appears somewhere in the output.
func TestRenderTable_showsAllFourFields(t *testing.T) {
	p := detailscmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		ContactEmail:    "hi@example.com",
		ContactPhone:    "+1 555 0100",
		ContactWebsite:  "https://x.example",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "hi@example.com", "+1 555 0100", "https://x.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderMarkdown_showsAllFourFields asserts the markdown view
// surfaces the same four fields. Per docs/DESIGN.md §7, markdown for a
// single-record read is a `- **Field**: value` list rather than a GFM
// table.
func TestRenderMarkdown_showsAllFourFields(t *testing.T) {
	p := detailscmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		ContactEmail:    "hi@example.com",
		ContactPhone:    "+1 555 0100",
		ContactWebsite:  "https://x.example",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "hi@example.com", "+1 555 0100", "https://x.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

// TestGroup_bareDetailsIsPureNoun asserts the ADR-0019 shape: `apps details`
// is a pure grouping noun: the bare command prints help and never reads (the
// read lives under `view`), and `set` stays present. Its RunE is the shared
// grouping RunE (kernel.GroupRunE: help when bare, loud exit-2 misuse on an
// unknown subcommand), NOT a business reader. This is the structural guard for
// #166 (a verb-less read would be a regression) plus the loud-failure contract.
func TestGroup_bareDetailsIsPureNoun(t *testing.T) {
	group := detailscmd.NewCommand(kernel.Boot{})
	// A grouping noun carries no business Run; its RunE only helps or rejects.
	if group.Run != nil {
		t.Error("bare `apps details` must not carry a business Run (it is a grouping noun)")
	}
	if group.RunE == nil {
		t.Fatal("bare `apps details` must have the grouping RunE (help when bare, loud on unknown subcommand)")
	}
	// Bare invocation prints help and succeeds: it never performs a read.
	group.SetOut(io.Discard)
	if err := group.RunE(group, nil); err != nil {
		t.Errorf("bare `apps details` should print help and succeed, got err=%v", err)
	}
	// An unknown subcommand is rejected loudly, naming the command, not
	// silently helped with exit 0.
	if err := group.RunE(group, []string{"nonesuch"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown subcommand of `apps details` should be rejected naming the command; got err=%v", err)
	}
	var hasView, hasSet bool
	for _, sub := range group.Commands() {
		switch sub.Name() {
		case "view":
			hasView = true
		case "set":
			hasSet = true
		}
	}
	if !hasView {
		t.Error("`apps details` must hold a `view` subcommand (the record read)")
	}
	if !hasSet {
		t.Error("`apps details` must keep its `set` subcommand (the write)")
	}
}

// TestViewCommand_hasReadSurface asserts `apps details view` is wired as the
// read: it runs (has a RunE) and exposes --package.
func TestViewCommand_hasReadSurface(t *testing.T) {
	view := detailscmd.NewViewCommand(kernel.Boot{})
	if view.RunE == nil {
		t.Error("`apps details view` must have a RunE (it reads the record)")
	}
	if view.Flags().Lookup("package") == nil {
		t.Error("`apps details view` must expose --package")
	}
	// Per the repo contract, every command supports --output with table/json/
	// markdown: lock that on the read surface too.
	out := view.Flags().Lookup("output")
	if out == nil {
		t.Fatal("`apps details view` must expose --output")
	}
	for _, want := range []string{"table", "json", "markdown"} {
		if !strings.Contains(out.Usage, want) {
			t.Errorf("--output usage missing %q; got %q", want, out.Usage)
		}
	}
}
