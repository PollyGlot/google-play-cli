// Package create_test exercises `gplay tracks create` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote command's test harness so a single seam proves the auth +
// Edit lifecycle wiring for tracks create too.
package create_test

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

	"github.com/PollyGlot/google-play-cli/commands/tracks/create"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// createRT terminates the OAuth2 /token exchange and routes every
// androidpublisher call the create flow makes: edits.insert,
// tracks.create, edits.commit, edits.delete. trackCreateStatus lets a
// test force a non-2xx on the POST .../tracks (the "track already
// exists" case).
type createRT struct {
	t                 *testing.T
	editID            string
	trackCreateStatus int
	trackCreateResp   string

	mu             sync.Mutex
	calls          []string
	tokenHits      int
	trackCreateReq []byte
}

func (r *createRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/tracks"):
		body, _ := io.ReadAll(req.Body)
		r.trackCreateReq = body
		status := r.trackCreateStatus
		if status == 0 {
			status = 200
		}
		resp := r.trackCreateResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(status, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
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

// signedSAJSON returns a minimal but well-formed service-account JSON
// with a real RSA key — enough for token.Source to mint a signed JWT.
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

// TestRun_dryRun_noHTTP asserts --dry-run previews the TrackConfig
// without auth or any HTTP call, and the human renders mention the
// hardcoded CLOSED_TESTING type.
func TestRun_dryRun_noHTTP(t *testing.T) {
	rt := &createRT{t: t}
	rc, _ := newRC(t, rt)

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on dry-run")
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", rt.calls)
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(table.String(), "CLOSED_TESTING") {
		t.Errorf("table output = %q, want it to mention CLOSED_TESTING", table.String())
	}
}

// TestRun_happyPath_createsClosedTrack asserts the full vertical slice:
// /token exchange, then the open → create → commit sequence, with the
// create request carrying type=CLOSED_TESTING, and the --output json
// render being the raw tracks.create response verbatim (ADR-0003).
func TestRun_happyPath_createsClosedTrack(t *testing.T) {
	rt := &createRT{
		t:               t,
		editID:          "edit-create-cli",
		trackCreateResp: `{"track":"qa-team","releases":[]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"})
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
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-create-cli/tracks",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-create-cli:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	if !strings.Contains(string(rt.trackCreateReq), `"type":"CLOSED_TESTING"`) {
		t.Errorf("create request body = %s, want type=CLOSED_TESTING", rt.trackCreateReq)
	}

	// ADR-0003: --output json must be the raw tracks.create response.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(rt.trackCreateResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.create payload = %s", got, rt.trackCreateResp)
	}
}

// TestRun_createOnExistingTrack_exit30 asserts that creating a track
// that already exists surfaces the API 400 verbatim (exit 30 via
// StatusToExitCode) — gplay does not fake idempotency — and that the
// failed Edit is auto-discarded (DELETE) since keep-edit-on-failure is
// off by default.
func TestRun_createOnExistingTrack_exit30(t *testing.T) {
	rt := &createRT{
		t:                 t,
		editID:            "edit-create-cli",
		trackCreateStatus: 400,
		trackCreateResp:   `{"error":{"code":400,"message":"Track already exists.","errors":[{"reason":"badRequest"}]}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"})
	if err == nil {
		t.Fatal("Run returned nil error; want the API 400 surfaced")
	}
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
	}

	// The Edit must have been discarded after the failure.
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected an edits.delete (auto-discard) after the failure, calls=%v", rt.calls)
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: the expected flags exist, the deliberately-absent ones
// (--type / --form-factor / --confirm) do not, and Use carries the
// positional <name>.
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := create.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"dry-run",
		"keep-edit-on-failure",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	for _, name := range []string{"type", "form-factor", "confirm"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("cobra command has unexpected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "create <name>" {
		t.Errorf("cmd.Use = %q, want %q", got, "create <name>")
	}
}

// TestRun_happyPath_emitsConfirmationOnStderr asserts a committed track create
// prints a single ✓ line on stderr (DESIGN §8) naming the new track, alongside
// the stdout payload.
func TestRun_happyPath_emitsConfirmationOnStderr(t *testing.T) {
	rt := &createRT{
		t:               t,
		editID:          "edit-create-cli",
		trackCreateResp: `{"track":"qa-team","releases":[]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "qa-team") {
		t.Errorf("track create ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	rt := &createRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
