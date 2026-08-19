// Package list_test exercises `gplay testers list` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// tracks view / promote harness so a single seam proves the auth +
// read-only Edit lifecycle wiring for testers list too.
package list_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/testers/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// listRT terminates the OAuth2 /token exchange and routes every
// androidpublisher call needed by a read-only testers listing:
// edits.insert, testers.get, edits.delete (the read-only discard).
type listRT struct {
	t              *testing.T
	editID         string
	testersGetResp string

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *listRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/testers/"):
		return jsonResp(200, r.testersGetResp), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// signedSAJSON returns a minimal but well-formed service-account JSON with
// a real RSA key: enough for token.Source to mint a signed JWT.
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
	return rc, &stdout
}

// TestRun_happyPath_listsGroups asserts the full read-only vertical slice:
// /token exchange precedes edits.insert, then testers.get reads the
// audience, then the Edit is discarded (edits.delete): never committed.
// --output json must be the raw testers.get body verbatim (ADR-0003).
func TestRun_happyPath_listsGroups(t *testing.T) {
	rt := &listRT{
		t:              t,
		editID:         "edit-testers-cli",
		testersGetResp: `{"googleGroups":["qa@googlegroups.com","beta@googlegroups.com"]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := list.Run(rc, list.Input{
		Package: "com.example.app",
		Track:   "qa-team",
	})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-testers-cli/testers/qa-team",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-testers-cli",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// ADR-0003: --output json must be API pass-through: the raw
	// testers.get response body verbatim.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(rt.testersGetResp) {
		t.Errorf("JSON output = %s\nwant raw testers.get payload = %s", got, rt.testersGetResp)
	}
}

// TestRun_missingTrack_exit2_noHTTP asserts --track is required: the CLI
// must short-circuit with a usage error (exit 2) before any HTTP call.
func TestRun_missingTrack_exit2_noHTTP(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: --package, --track, --output exist and the command is
// named "list".
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := list.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "track", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "list" {
		t.Errorf("cmd.Use = %q, want %q", got, "list")
	}
}
