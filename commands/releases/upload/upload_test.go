// Package upload_test exercises `gplay releases upload` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. The
// RoundTripper sees both the /token exchange and the androidpublisher
// API calls, so a single seam proves the auth + Edit lifecycle wiring.
package upload_test

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// uploadRT is the RoundTripper command-level upload tests inject. It
// terminates the OAuth2 /token exchange (so token.Source produces a
// usable Bearer token) and routes every androidpublisher call needed
// by the orchestrator: edits.insert, bundles.upload, tracks.update,
// edits.commit, edits.delete. The fake response bodies are the minimum
// the orchestrator's response parsers accept.
//
// The recorded paths let tests assert that, e.g., a confirm-guarded
// production publish never hits the wire at all.
type uploadRT struct {
	t                  *testing.T
	editID             string
	versionCode        int
	trackUpdateRawResp string
	// trackUpdateStatus, when >= 400, makes the tracks.update PUT fail with
	// that status (carrying a Google error envelope) so tests can exercise
	// the track-not-found hint path. 0 (the default) means a 200 success.
	trackUpdateStatus int

	mu             sync.Mutex
	calls          []string
	tokenHits      int
	trackUpdateReq []byte
}

func (r *uploadRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return jsonResp(200, body), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/bundles"):
		return jsonResp(200, fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, r.versionCode)), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/tracks/"):
		body, _ := io.ReadAll(req.Body)
		r.trackUpdateReq = body
		if r.trackUpdateStatus >= 400 {
			return jsonResp(r.trackUpdateStatus, `{"error":{"code":404,"message":"Track not found."}}`), nil
		}
		resp := r.trackUpdateRawResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(200, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, r.editID)), nil
	}
	r.t.Fatalf("uploadRT: unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// signedSAJSON generates a service-account JSON whose private_key is a
// fresh RSA key so the oauth2 library can sign the exchange JWT in
// tests. Mirrors the helper in doctor_test.go.
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

// writeFakeAAB creates a non-empty file with a .aab extension. The
// RoundTripper does not validate the bytes — bundles.Upload just needs
// os.Open to succeed.
func writeFakeAAB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(p, []byte("fake-aab-content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// newRC builds a RunContext with the injected RoundTripper installed as
// the oauth2.HTTPClient seam and a parsed Account loaded from
// signedSAJSON. The kernel-level Run is invoked directly so the test
// covers the full Run → orchestrator → play/* round trip.
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

// TestRun_internalTrack_happyPath_hitsTokenAndAndroidPublisher exercises
// the full vertical slice. Asserts both that the orchestrator returned
// a usable Renderable and that the injected RoundTripper saw the
// /token exchange in front of the androidpublisher calls.
func TestRun_internalTrack_happyPath_hitsTokenAndAndroidPublisher(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
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
	// The /token call must precede the androidpublisher edits.insert.
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
}

// TestRun_mutualExclusion_releaseNotesAndDir_exit2_noHTTP asserts the
// CLI-misuse guard fires before any HTTP. The RoundTripper must record
// zero calls so a buggy fix that defers the check past auth setup is
// caught here.
func TestRun_mutualExclusion_releaseNotesAndDir_exit2_noHTTP(t *testing.T) {
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         "/tmp/whatever.aab",
		ReleaseNotes:    "hello",
		ReleaseNotesDir: "/tmp/notes",
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(rt.calls), rt.calls)
	}
}

// TestRun_productionPublishWithoutConfirm_exit2_noHTTP asserts that a
// production --complete without --confirm hits the orchestrator's
// ConfirmRequiredError guard before any HTTP — including the /token
// exchange. The oauth2 library is lazy, so a buggy refactor that
// pre-mints a token would show up as a non-zero call count here.
func TestRun_productionPublishWithoutConfirm_exit2_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package:  "com.example.app",
		Track:    "production",
		AABPath:  aab,
		Complete: true,
		Confirm:  false,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want ConfirmRequiredError")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("RoundTripper saw %d calls on confirm-guarded error: %v", len(rt.calls), rt.calls)
	}
}

// TestRun_productionPublishWithConfirm_succeedsAndHitsAPI asserts that
// --confirm unblocks the production publish and the RoundTripper sees
// the full Edit lifecycle on the production track. The tracks.update
// payload must carry status=completed and userFraction=1.0.
func TestRun_productionPublishWithConfirm_succeedsAndHitsAPI(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"completed","versionCodes":["200"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)

	r, err := upload.Run(rc, upload.Input{
		Package:  "com.example.app",
		Track:    "production",
		AABPath:  aab,
		Complete: true,
		Confirm:  true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on confirmed production publish")
	}
	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"track":"production"`) {
		t.Errorf("tracks.update body = %s, want track=production", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("tracks.update body = %s, want userFraction=1.0", body)
	}
}

// TestRun_uploadToMissingClosedTrack_hintsTracksCreate asserts that an
// upload whose --track has not been created yet (tracks.update 404) fails
// with a `gplay tracks create <name>` hint, preserves the underlying exit
// code (30, not rewritten by the hint), and never auto-creates the track —
// gplay only ever PUTs tracks.update, never POSTs a fresh track on upload.
func TestRun_uploadToMissingClosedTrack_hintsTracksCreate(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                 t,
		editID:            "edit-miss",
		versionCode:       142,
		trackUpdateStatus: http.StatusNotFound,
	}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "qa-team",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want a track-not-found hint")
	}
	if !strings.Contains(err.Error(), "gplay tracks create qa-team") {
		t.Errorf("error %q is missing the `gplay tracks create qa-team` hint", err.Error())
	}
	if code := exit.For(err); code != 30 {
		t.Errorf("exit.For(err) = %d, want 30 (underlying *api.Error preserved); err=%v", code, err)
	}
	// No auto-create: gplay must never POST a fresh track as a side effect of
	// an upload — only PUT tracks.update against the (expected-to-exist) track.
	for _, c := range rt.calls {
		if c == "POST /androidpublisher/v3/applications/com.example.app/edits/edit-miss/tracks" {
			t.Errorf("upload auto-created a track (saw %q); it must only PUT tracks.update", c)
		}
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring. It does not exercise Run end-to-end (the kernel-level
// tests above do that) — its job is to catch a flag-plumbing regression
// where a future refactor of NewCommand drops or renames a flag and
// the Input struct silently goes unwired.
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := upload.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"track",
		"release-notes",
		"release-notes-dir",
		"draft",
		"complete",
		"staged",
		"keep-edit-on-failure",
		"confirm",
		"dry-run",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "upload <aab>" {
		t.Errorf("cmd.Use = %q, want %q", got, "upload <aab>")
	}
}

// TestRun_happyPath_emitsConfirmationOnStderr asserts a committed upload
// prints a single ✓ success line on stderr (DESIGN §8) carrying the
// versionCode, track, and status — in addition to the stdout payload.
func TestRun_happyPath_emitsConfirmationOnStderr(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: aab}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("stderr missing ✓ confirmation line:\n%s", got)
	}
	for _, want := range []string{"142", "internal", "completed"} {
		if !strings.Contains(got, want) {
			t.Errorf("✓ line %q missing %q", got, want)
		}
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓:
// the marker means committed, and a dry-run only previews to stdout.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: aab, DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}

// TestRun_inProgressRollout_confirmationShowsUserFractionPercent asserts the
// ✓ line renders userFraction as a percentage when status is inProgress —
// the one status where the fraction informs (DESIGN §8).
func TestRun_inProgressRollout_confirmationShowsUserFractionPercent(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-staged",
		versionCode:        77,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"77","status":"inProgress","versionCodes":["77"],"userFraction":0.05}]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "beta", AABPath: aab, StagedFraction: 0.05, StagedFractionSet: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "5%") {
		t.Errorf("✓ line should render userFraction 0.05 as 5%%; stderr=%q", stderr.String())
	}
}
