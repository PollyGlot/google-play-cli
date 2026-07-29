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
	"errors"
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
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/deobfuscationFiles/"):
		// Resumable initiate for the mapping upload: session URI in Location.
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-" + r.editID
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Location": []string{loc}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/deobfuscationFiles/"):
		return jsonResp(200, `{"deobfuscationFile":{"symbolType":"proguard"}}`), nil
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/bundles"):
		// Resumable initiate: return the session URI (same /bundles path) in
		// Location; the PUT below carries the single chunk and the versionCode.
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-" + r.editID
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Location": []string{loc}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	case req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/bundles"):
		return jsonResp(200, fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, r.versionCode)), nil
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/apks"):
		// Resumable initiate for the APK upload: session URI in Location.
		loc := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path + "?upload_id=session-" + r.editID
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Location": []string{loc}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	case req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/apks"):
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
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
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

// TestRun_productionPublishWithoutConfirm_exit3_noHTTP asserts that a
// production --complete without --confirm hits the orchestrator's confirm
// guard before any HTTP — including the /token exchange. The oauth2 library
// is lazy, so a buggy refactor that pre-mints a token would show up as a
// non-zero call count here. The refusal is exit 3 (safety flag required,
// docs/DESIGN.md §9), not the generic usage exit 2 (#408).
func TestRun_productionPublishWithoutConfirm_exit3_noHTTP(t *testing.T) {
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
		t.Fatal("Run returned nil error; want a --confirm safety refusal")
	}
	if got := exit.For(err); got != 3 {
		t.Errorf("exit.For(err) = %d, want 3; err=%v", got, err)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
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
		"mapping",
		"format",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "upload <artifact>" {
		t.Errorf("cmd.Use = %q, want %q", got, "upload <artifact>")
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

// writeFakeMapping creates a non-empty mapping.txt. The RoundTripper does
// not validate the bytes — mappings.Upload just needs os.Open to succeed.
func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestRun_withMapping_uploadsMappingInSameEditAndConfirms asserts that
// `releases upload --mapping <file>` uploads the ProGuard mapping inside
// the same Edit (keyed by the bundle's versionCode) and that the ✓
// confirmation line reflects the mapping (#250).
func TestRun_withMapping_uploadsMappingInSameEditAndConfirms(t *testing.T) {
	aab := writeFakeAAB(t)
	mapping := writeFakeMapping(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
		Mapping: mapping,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
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
	if !strings.Contains(stderr.String(), "mapping") {
		t.Errorf("✓ line should mention the uploaded mapping; stderr=%q", stderr.String())
	}
}

// writeFakeAPK creates a non-empty .apk file. The RoundTripper does not
// validate the bytes — apks.Upload just needs os.Open to succeed.
func writeFakeAPK(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(p, []byte("fake-apk-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestRun_apkExtension_ridesApksUploadEndpoint asserts a .apk artifact is
// auto-detected and uploaded via edits.apks.upload (resumable POST-initiate
// + PUT to .../apks?uploadType=resumable)
// instead of bundles.upload, while the rest of the Edit lifecycle — insert,
// tracks.update, commit — is byte-for-byte the AAB pipeline (ADR-0036).
func TestRun_apkExtension_ridesApksUploadEndpoint(t *testing.T) {
	apk := writeFakeAPK(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-apk",
		versionCode:        91,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"91","status":"completed","versionCodes":["91"],"userFraction":1.0}]}`,
	}
	rc, _ := newRC(t, rt)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: apk,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-apk/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-apk:commit",
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

// TestRun_formatApkOverride_forcesApkPathForNonApkFilename asserts that
// --format apk routes a file whose extension is NOT .apk through the APK
// endpoint — the override wins over extension auto-detect (ADR-0030 parity).
func TestRun_formatApkOverride_forcesApkPathForNonApkFilename(t *testing.T) {
	// A file with a .bin extension the auto-detect could not classify.
	p := filepath.Join(t.TempDir(), "build.bin")
	if err := os.WriteFile(p, []byte("fake-apk-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-fmt",
		versionCode:        5,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"5","status":"completed","versionCodes":["5"]}]}`,
	}
	rc, _ := newRC(t, rt)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: p,
		Format:  "apk",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sawApks := false
	for _, c := range rt.calls {
		if strings.HasSuffix(c, "/apks") {
			sawApks = true
		}
		if strings.HasSuffix(c, "/bundles") {
			t.Errorf("--format apk still hit the bundles endpoint: %q", c)
		}
	}
	if !sawApks {
		t.Errorf("--format apk did not hit the apks endpoint; calls=%v", rt.calls)
	}
}

// TestRun_unknownExtension_noFormat_exit2_noHTTP asserts that an artifact
// with an unrecognized extension and no --format is a usage error (exit 2)
// before any HTTP — mirroring the `releases sharing upload` message.
func TestRun_unknownExtension_noFormat_exit2_noHTTP(t *testing.T) {
	p := filepath.Join(t.TempDir(), "build.bin")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: p,
	})
	if err == nil {
		t.Fatal("Run accepted an unknown-extension artifact without --format")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "cannot tell APK from AAB by extension") {
		t.Errorf("error %q is missing the sharing-parity extension message", err.Error())
	}
	if len(rt.calls) != 0 {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(rt.calls), rt.calls)
	}
}

// TestRun_apkWithMapping_uploadsMappingAgainstApkVersionCode asserts
// --mapping works for an APK unchanged: the deobfuscation file is POSTed
// against the APK's versionCode in the same Edit (ADR-0036).
func TestRun_apkWithMapping_uploadsMappingAgainstApkVersionCode(t *testing.T) {
	apk := writeFakeAPK(t)
	mapping := writeFakeMapping(t)
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-apk",
		versionCode:        91,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"91","status":"completed","versionCodes":["91"]}]}`,
	}
	rc, _ := newRC(t, rt)

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: apk,
		Mapping: mapping,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks/91/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks/91/deobfuscationFiles/proguard",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-apk/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-apk:commit",
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
