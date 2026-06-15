// Package rollout_test exercises the rollout state-machine commands
// (rollout / halt / resume / complete) at the kernel level: a RunContext
// built by hand, a RoundTripper injected via the oauth2.HTTPClient context
// key, and the per-verb Run functions invoked directly. Mirrors the
// upload / promote command harness so one seam proves the auth + Edit
// lifecycle wiring for the whole family.
package rollout_test

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

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// stateRT terminates the OAuth2 /token exchange and routes every
// androidpublisher call the state machine makes: edits.insert, tracks.get,
// tracks.update, edits.commit, edits.delete.
type stateRT struct {
	t                  *testing.T
	editID             string
	trackGetResp       string
	trackUpdateRawResp string

	mu             sync.Mutex
	calls          []string
	tokenHits      int
	trackUpdateReq []byte
}

func (r *stateRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tracks/"):
		return jsonResp(200, r.trackGetResp), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/tracks/"):
		body, _ := io.ReadAll(req.Body)
		r.trackUpdateReq = body
		resp := r.trackUpdateRawResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(200, resp), nil
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

// signedSAJSON returns a minimal but well-formed service-account JSON with
// a real RSA key — enough for token.Source to mint a signed JWT.
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

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
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
	return rc
}

const oneInProgressRelease = `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.05}]}`

// TestRunRollout_happyPath_fullSequence asserts the full vertical slice for
// rollout: /token precedes the canonical four-call sequence, and the
// tracks.update body carries the requested fraction + inProgress status.
func TestRunRollout_happyPath_fullSequence(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-rollout-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2}]}`,
	}
	rc := newRC(t, rt)

	r, err := rollout.RunRollout(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		To:      "0.2",
		ToSet:   true,
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("RunRollout: %v", err)
	}
	if r == nil {
		t.Fatal("RunRollout returned nil Renderable on happy path")
	}
	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli/tracks/production",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli/tracks/production",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"inProgress"`) || !strings.Contains(body, `"userFraction":0.2`) {
		t.Errorf("tracks.update body = %s, want inProgress at 0.2", body)
	}

	// ADR-0003: --output json is API pass-through (raw tracks.update body).
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(rt.trackUpdateRawResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.update payload = %s", got, rt.trackUpdateRawResp)
	}
}

// TestRunHalt_happyPath asserts halt maps to orchestrator.Halt and the
// wire payload carries status=halted.
func TestRunHalt_happyPath(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-halt-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
	}
	rc := newRC(t, rt)

	if _, err := rollout.RunHalt(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
	}); err != nil {
		t.Fatalf("RunHalt: %v", err)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"halted"`) {
		t.Errorf("tracks.update body = %s, want status=halted", body)
	}
}

// TestRunResume_happyPath asserts resume flips a halted release to
// inProgress with the fraction preserved.
func TestRunResume_happyPath(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-resume-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
		trackUpdateRawResp: `{}`,
	}
	rc := newRC(t, rt)

	if _, err := rollout.RunResume(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	}); err != nil {
		t.Fatalf("RunResume: %v", err)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"inProgress"`) || !strings.Contains(body, `"userFraction":0.05`) {
		t.Errorf("tracks.update body = %s, want inProgress at preserved 0.05", body)
	}
}

// TestRunComplete_happyPath asserts complete ramps to completed/1.0.
func TestRunComplete_happyPath(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-complete-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{}`,
	}
	rc := newRC(t, rt)

	if _, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	}); err != nil {
		t.Fatalf("RunComplete: %v", err)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("tracks.update body = %s, want completed at 1.0", body)
	}
}

// TestRun_productionWriteWithoutConfirm_returnsExit2 asserts the production
// confirm gate at the CLI: complete on production without --confirm refuses
// (exit 2) before any HTTP.
func TestRun_productionWriteWithoutConfirm_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		// Confirm omitted → must refuse before any HTTP.
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before confirm guard, saw: %v", rt.calls)
	}
}

// TestRunRollout_missingTo_returnsExit2 asserts that omitting --to is a CLI
// misuse caught before any HTTP, with a message naming the flag.
func TestRunRollout_missingTo_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		// ToSet false → --to not supplied.
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
	if !strings.Contains(err.Error(), "--to") {
		t.Errorf("err = %q, want it to mention --to", err.Error())
	}
}

// TestRunRollout_nonNumericTo_returnsExit2 asserts AC6's non-numeric case:
// --to abc is a CLI misuse (exit 2), not cobra's exit-1 parse error.
func TestRunRollout_nonNumericTo_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		To:      "abc",
		ToSet:   true,
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRunRollout_outOfRangeTo_returnsExit2 asserts the range guard (AC6):
// --to 1.5 is rejected with exit 2 and a range hint, before any HTTP.
func TestRunRollout_outOfRangeTo_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		To:      "1.5",
		ToSet:   true,
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_missingTrack_returnsExit2 asserts --track is required across the
// family (checked here via complete).
func TestRun_missingTrack_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_returnsExit2 asserts that with neither --package
// nor a project pin, the command refuses before any HTTP.
func TestRun_missingPackage_returnsExit2(t *testing.T) {
	rt := &stateRT{t: t}
	rc := newRC(t, rt)

	_, err := rollout.RunHalt(rc, rollout.Input{
		Track: "production",
	})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRenderTable_showsUserFractionForHalted asserts the table renderer
// surfaces the preserved fraction on a halted release, so the operator can
// see "halted at 5%".
func TestRenderTable_showsUserFractionForHalted(t *testing.T) {
	p := rollout.Payload{Result: &orchestrator.Result{
		VersionCode:  142,
		Track:        "production",
		Status:       "halted",
		UserFraction: 0.05,
		ReleaseName:  "142",
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "userFraction") || !strings.Contains(out, "0.05") {
		t.Errorf("table output = %q, want a userFraction row showing 0.05 for halted", out)
	}
	if !strings.Contains(out, "halted") {
		t.Errorf("table output = %q, want the halted status", out)
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != want {
		t.Errorf("ExitCode() = %d, want %d", coder.ExitCode(), want)
	}
}

// confirmStderr runs fn with a stderr buffer wired onto rc and returns what
// landed there — the shared setup for the ✓-confirmation tests below.
func confirmStderr(t *testing.T, rt http.RoundTripper, fn func(rc *kernel.RunContext) error) string {
	t.Helper()
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if err := fn(rc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return stderr.String()
}

// TestRunRollout_emitsConfirmationWithUserFractionPercent asserts a committed
// rollout prints a ✓ line on stderr (DESIGN §8) naming the track and rendering
// the new userFraction as a percentage (status inProgress).
func TestRunRollout_emitsConfirmationWithUserFractionPercent(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-rollout-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2}]}`,
	}
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", To: "0.2", ToSet: true, Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("stderr missing ✓ confirmation line:\n%s", got)
	}
	for _, want := range []string{"production", "20%"} {
		if !strings.Contains(got, want) {
			t.Errorf("✓ line %q missing %q", got, want)
		}
	}
}

// TestRunHalt_emitsConfirmation asserts halt prints a ✓ line naming the track
// and the halted status (no userFraction — only inProgress shows it).
func TestRunHalt_emitsConfirmation(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-halt-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
	}
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunHalt(rc, rollout.Input{Package: "com.example.app", Track: "production"})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "halted") || !strings.Contains(got, "production") {
		t.Errorf("halt ✓ line wrong:\n%s", got)
	}
}

// TestRunResume_emitsConfirmation asserts resume prints a ✓ line.
func TestRunResume_emitsConfirmation(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-resume-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.05}]}`,
	}
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunResume(rc, rollout.Input{Package: "com.example.app", Track: "production", Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "resumed") || !strings.Contains(got, "production") {
		t.Errorf("resume ✓ line wrong:\n%s", got)
	}
}

// TestRunComplete_emitsConfirmation asserts complete prints a ✓ line.
func TestRunComplete_emitsConfirmation(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-complete-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunComplete(rc, rollout.Input{Package: "com.example.app", Track: "production", Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "completed") || !strings.Contains(got, "production") {
		t.Errorf("complete ✓ line wrong:\n%s", got)
	}
}

// TestRunRollout_dryRun_noConfirmation asserts --dry-run never emits a ✓.
func TestRunRollout_dryRun_noConfirmation(t *testing.T) {
	rt := &stateRT{t: t, editID: "edit-dry", trackGetResp: oneInProgressRelease}
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", To: "0.2", ToSet: true, DryRun: true})
		return err
	})
	if strings.Contains(got, "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", got)
	}
}
