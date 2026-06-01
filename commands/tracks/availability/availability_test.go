// Package availability_test exercises `gplay tracks availability` at the
// kernel level: a RunContext built by hand, a RoundTripper injected via
// the oauth2.HTTPClient context key, and Run invoked directly. Mirrors
// the tracks-status harness — the transport FAILS on any PUT/:commit,
// because reading Country availability is read-only (open Edit →
// countryAvailability.get → discard, never commit). Availability is also
// read-only at the API level: there is no writer to test.
package availability_test

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

	"github.com/PollyGlot/google-play-cli/commands/tracks/availability"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// availRT terminates the OAuth2 /token exchange and routes the
// availability read sequence: edits.insert, countryAvailability.get,
// edits.delete. It has NO PUT/:commit branch: reaching one means the
// command tried to mutate state, which a read-only command must never do
// — so the transport fails the test.
type availRT struct {
	t      *testing.T
	editID string
	body   string
	code   int // 0 → 200 on countryAvailability.get

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *availRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/countryAvailability/"):
		code := r.code
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.body), nil
	}
	r.t.Fatalf("unexpected request (tracks availability is read-only): %s %s", req.Method, req.URL)
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
// then edits.insert, countryAvailability.get(track), edits.delete (no
// commit). The payload carries syncWithProduction, restOfWorld, and the
// country codes; --output json emits the countryavailability.get body
// verbatim (clean ADR-0003 pass-through — a single endpoint).
func TestRun_happyPath(t *testing.T) {
	body := `{"syncWithProduction":false,"restOfWorld":true,"countries":[{"countryCode":"US"},{"countryCode":"GB"}]}`
	rt := &availRT{t: t, editID: "edit-avail", body: body}
	rc, _ := newRC(t, rt)

	r, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "production"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-avail/countryAvailability/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-avail",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// JSON pass-through: the countryavailability.get body verbatim.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(body) {
		t.Errorf("JSON output = %s\nwant body verbatim = %s", got, body)
	}
}

// TestRun_missingTrack_exit2_noHTTP asserts --track is REQUIRED (no
// implicit production default): absence short-circuits with exit 2 before
// any HTTP call.
func TestRun_missingTrack_exit2_noHTTP(t *testing.T) {
	rt := &availRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2 (missing --track)", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_exit2 asserts that with neither --package nor a
// pinned project, the command short-circuits with exit 2 before any HTTP.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &availRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := availability.Run(rc, availability.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_usesPin_whenNoFlag asserts that without --package the command
// falls back to rc.Resolved.Pin.
func TestRun_usesPin_whenNoFlag(t *testing.T) {
	rt := &availRT{
		t:      t,
		editID: "edit-pin",
		body:   `{"syncWithProduction":true,"restOfWorld":false,"countries":[]}`,
	}
	rc, _ := newRC(t, rt)
	rc.Resolved.Pin = "com.pinned.app"

	if _, err := availability.Run(rc, availability.Input{Track: "beta"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range rt.calls {
		if strings.Contains(c, "com.pinned.app") {
			return
		}
	}
	t.Errorf("expected calls scoped to com.pinned.app, got: %v", rt.calls)
}

// TestRun_get403_exit11 asserts a 403 on countryAvailability.get bubbles
// up as exit 11 (authorization).
func TestRun_get403_exit11(t *testing.T) {
	rt := &availRT{
		t:      t,
		editID: "edit-403",
		code:   403,
		body:   `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
}

// TestRun_get404_exit30_tracksListHint asserts a 404 (unknown track or
// package) maps to exit 30 with a hint pointing at `gplay tracks list`.
func TestRun_get404_exit30_tracksListHint(t *testing.T) {
	rt := &availRT{
		t:      t,
		editID: "edit-404",
		code:   404,
		body:   `{"error":{"code":404,"message":"track not found"}}`,
	}
	rc, _ := newRC(t, rt)
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "no-such-track"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "gplay tracks list") {
		t.Errorf("404 error = %q, want it to mention 'gplay tracks list'", err.Error())
	}
}

// TestRenderTable_showsAvailabilityFields asserts the table view surfaces
// syncWithProduction, restOfWorld, and the country codes.
func TestRenderTable_showsAvailabilityFields(t *testing.T) {
	p := availability.Payload{
		Track:              "production",
		SyncWithProduction: false,
		RestOfWorld:        true,
		Countries:          []string{"US", "GB", "FR"},
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"production", "US", "GB", "FR"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderMarkdown_showsAvailabilityFields asserts the markdown view
// surfaces the same fields.
func TestRenderMarkdown_showsAvailabilityFields(t *testing.T) {
	p := availability.Payload{
		Track:              "production",
		SyncWithProduction: true,
		RestOfWorld:        false,
		Countries:          []string{"US", "GB"},
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"production", "US", "GB"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

// TestNewCommand_requiresTrackFlag is a thin smoke test for the cobra
// wiring: --track and --package exist, --output exists, and the command
// is named "availability".
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := availability.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "track", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "availability" {
		t.Errorf("cmd.Use = %q, want %q", got, "availability")
	}
}
