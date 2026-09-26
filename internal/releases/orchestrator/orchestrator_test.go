// Package orchestrator_test exercises the upload orchestration end-to-end
// through a RoundTripper-mocked Google Play Developer API. Each test
// drives one behavior: the tracer bullet here is the canonical
// four-call sequence on a non-production track.
package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// handler answers one endpoint of a fake in place of its happy-path default:
// the status (0 → 200) and body for the recorded call.
type handler func(c testkit.Call) (int, string)

// playAPI configures the androidpublisher endpoints involved in an upload:
// edits.insert, edits.details.get, bundles.upload, tracks.update,
// edits.commit, and the cleanup edits.delete. Each endpoint has a default
// happy-path response; tests override any specific handler to inject a
// failure or to fail on a call that must not happen.
type playAPI struct {
	editID             string
	versionCode        int
	defaultLanguage    string // returned by the default details handler
	trackUpdateRawResp string

	// Optional handler overrides. nil → use the default happy-path
	// response for that endpoint.
	insertHandler  handler
	detailsHandler handler
	bundleHandler  handler
	deobfHandler   handler
	trackHandler   handler
	commitHandler  handler
	deleteHandler  handler
}

// newPlay returns the Fake that records every call for assertions and the
// transport to inject. The wrapper only adds the session URI (Location) to a
// successful resumable initiate, which a responder cannot express.
func newPlay(a playAPI) (*testkit.Fake, http.RoundTripper) {
	override := func(h handler, c testkit.Call) (int, string, bool) {
		status, body := h(c)
		return status, body, true
	}
	f := testkit.NewFake(testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			if a.insertHandler != nil {
				return override(a.insertHandler, c)
			}
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/details"):
			if a.detailsHandler != nil {
				return override(a.detailsHandler, c)
			}
			lang := a.defaultLanguage
			if lang == "" {
				lang = "en-US"
			}
			return http.StatusOK, fmt.Sprintf(`{"defaultLanguage":%q,"contactEmail":"x@example.com"}`, lang), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			if a.deleteHandler != nil {
				return override(a.deleteHandler, c)
			}
			return http.StatusNoContent, "", true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/bundles"):
			if a.bundleHandler != nil {
				return override(a.bundleHandler, c)
			}
			// Resumable initiate: the wrapper hands back a session URI (the
			// same /bundles path, with an upload_id query) in Location. The
			// helper then PUTs the artifact chunk(s) there. A fake AAB is a
			// few bytes, so it is a single chunk; the PUT below returns the
			// versionCode body.
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/bundles"):
			// Resumable chunk PUT (the final chunk carries the resource body).
			return http.StatusOK, fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, a.versionCode), true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/deobfuscationFiles/"):
			if a.deobfHandler != nil {
				return override(a.deobfHandler, c)
			}
			// Resumable initiate: session URI in Location. The mapping bytes
			// travel on the PUT chunk below.
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/deobfuscationFiles/"):
			return http.StatusOK, `{"deobfuscationFile":{"symbolType":"proguard"}}`, true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/"):
			if a.trackHandler != nil {
				return override(a.trackHandler, c)
			}
			if a.trackUpdateRawResp == "" {
				return http.StatusOK, `{}`, true
			}
			return http.StatusOK, a.trackUpdateRawResp, true
		case strings.HasSuffix(c.Path, ":commit"):
			if a.commitHandler != nil {
				return override(a.commitHandler, c)
			}
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, a.editID), true
		}
		return 0, "", false
	}, "Location", func(c testkit.Call, status int) string {
		// Resumable initiate: the session URI goes in Location.
		if status == http.StatusOK && c.Method == http.MethodPost &&
			(strings.Contains(c.Path, "/bundles") || strings.Contains(c.Path, "/deobfuscationFiles/")) {
			return "https://" + c.Host + c.Path + "?upload_id=session-" + a.editID
		}
		return ""
	}))
	return f, f
}

// apiCalls lists the recorded calls as "METHOD path".
func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// touched reports whether anything reached the transport, token exchange
// included.
func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

// lastBody returns the body of the last recorded call with method whose path
// contains fragment, nil when there is none.
func lastBody(f *testkit.Fake, method, fragment string) []byte {
	var body []byte
	for _, c := range f.Calls() {
		if c.Method == method && strings.Contains(c.Path, fragment) {
			body = c.Body
		}
	}
	return body
}

// trackUpdateReq returns the body of the last tracks.update PUT.
func trackUpdateReq(f *testkit.Fake) []byte { return lastBody(f, http.MethodPut, "/tracks/") }

// deobfReqBody returns the body of the last deobfuscation-file chunk PUT.
func deobfReqBody(f *testkit.Fake) []byte {
	return lastBody(f, http.MethodPut, "/deobfuscationFiles/")
}

// writeFakeAAB creates a non-empty file in t.TempDir() with a .aab
// extension. The fake transport does not validate the bytes: it just
// needs os.Open to succeed.
func writeFakeAAB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(p, []byte("fake-aab-content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_internalTrack_happyPath_setsCompletedRelease is the tracer
// bullet for orchestrator.Upload: a complete vertical slice through the
// upload flow on a non-production track. Asserts the canonical four-call
// sequence (edits.insert → bundles.upload → tracks.update → edits.commit)
// and that the track update carries status=completed and userFraction=1.0.
func TestUpload_internalTrack_happyPath_setsCompletedRelease(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// Canonical call sequence. bundles.upload is now a resumable upload:
	// a POST initiate followed by a PUT of the (single) chunk to the session
	// URI (same /bundles path), then tracks.update and the commit.
	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	calls := apiCalls(rt)
	if len(calls) != len(wantPaths) {
		t.Fatalf("got %d calls (%v), want %d", len(calls), calls, len(wantPaths))
	}
	for i, want := range wantPaths {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
	}

	// Track update payload carries status=completed, userFraction=1.0.
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("tracks.update body = %s, want userFraction=1.0", body)
	}

	// Result carries versionCode echoed by bundles.upload.
	if result.VersionCode != 142 {
		t.Errorf("VersionCode = %d, want 142", result.VersionCode)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Track != "internal" {
		t.Errorf("Track = %q, want internal", result.Track)
	}
}

// TestUpload_productionTrack_safeDefault_sendsDraftStatus asserts the
// ADR-0002 safe-default rule: target production with Status=Unspecified
// produces a draft release (no userFraction field).
func TestUpload_productionTrack_safeDefault_sendsDraftStatus(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"draft","versionCodes":["200"]}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		// Status: StatusUnspecified (default zero value) → safe-default applies.
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"draft"`) {
		t.Errorf("production safe-default: body = %s, want status=draft", body)
	}
	// userFraction must be omitted for a draft release (omitempty drops zero).
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("production safe-default: body = %s, want userFraction omitted for draft", body)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
	if result.UserFraction != 0 {
		t.Errorf("result.UserFraction = %v, want 0", result.UserFraction)
	}
}

// TestUpload_productionTrack_explicitCompleted_sendsCompletedFull asserts
// that --complete on production (Status=Completed) overrides the
// safe-default and ships completed/1.0.
func TestUpload_productionTrack_explicitCompleted_sendsCompletedFull(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod-c",
		versionCode:        201,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		Status:  orchestrator.StatusCompleted,
		Confirm: true, // production publish gate (see confirmRequired)
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("body = %s, want status=completed", body)
	}
	if !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("body = %s, want userFraction=1.0", body)
	}
	if result.Status != "completed" || result.UserFraction != 1.0 {
		t.Errorf("result = (%q, %v), want (completed, 1.0)", result.Status, result.UserFraction)
	}
}

// TestUpload_productionTrack_explicitStaged_sendsInProgressWithFraction
// asserts that --staged 0.05 (Status=InProgress + UserFraction=0.05) on
// production ships inProgress at 5%.
func TestUpload_productionTrack_explicitStaged_sendsInProgressWithFraction(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod-s",
		versionCode:        202,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:      "com.example.app",
		Track:        "production",
		AABPath:      aab,
		Status:       orchestrator.StatusInProgress,
		UserFraction: 0.05,
		Confirm:      true, // production publish gate (see confirmRequired)
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"inProgress"`) {
		t.Errorf("body = %s, want status=inProgress", body)
	}
	if !strings.Contains(body, `"userFraction":0.05`) {
		t.Errorf("body = %s, want userFraction=0.05", body)
	}
	if result.Status != "inProgress" || result.UserFraction != 0.05 {
		t.Errorf("result = (%q, %v), want (inProgress, 0.05)", result.Status, result.UserFraction)
	}
}

// TestUpload_bundleUploadFail_triggersEditDelete asserts the auto-discard
// safety net: when bundles.upload fails mid-Edit (e.g. malformed AAB),
// the orchestrator MUST call edits.delete to clean up the orphan Edit
// before propagating the error. A leaked Edit blocks the user's next
// publish for up to 24h, so this is a load-bearing behavior.
func TestUpload_bundleUploadFail_triggersEditDelete(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID: "edit-fail",
		bundleHandler: func(c testkit.Call) (int, string) {
			return 400, `{"error":{"code":400,"message":"malformed AAB"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Upload: want error after bundle failure, got nil")
	}

	sawDelete := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after bundle upload failure; calls = %v", apiCalls(rt))
	}
}

// TestUpload_bundleUploadFail_keepOnFailure_doesNotDelete asserts the
// --keep-edit-on-failure opt-out: when KeepEditOnFailure=true, a mid-Edit
// failure must NOT trigger edits.delete, and the returned error must
// carry the open Edit ID (wrapped in *edits.DanglingEditError) so the
// operator can `gplay edits discard` it manually.
func TestUpload_bundleUploadFail_keepOnFailure_doesNotDelete(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID: "edit-keep",
		bundleHandler: func(c testkit.Call) (int, string) {
			return 400, `{"error":{"code":400,"message":"malformed AAB"}}`
		},
		deleteHandler: func(c testkit.Call) (int, string) {
			t.Errorf("DELETE was called despite KeepEditOnFailure=true: %s", c.Path)
			return 204, ""
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:           "com.example.app",
		Track:             "internal",
		AABPath:           aab,
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Upload: want error after bundle failure, got nil")
	}

	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") {
			t.Errorf("KeepEditOnFailure=true but saw DELETE in calls: %v", apiCalls(rt))
		}
	}

	// The error must carry the open Edit ID so the operator can recover.
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError carrying the Edit ID", err, err)
	}
	if dangling.EditID != "edit-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-keep", dangling.EditID)
	}
}

// TestUpload_commitFail_triggersEditDelete asserts the auto-discard
// safety net extends to commit failures too: a 5xx on edits.commit
// (after a successful AAB upload + tracks.update) must call
// edits.delete to clean up the orphan Edit.
func TestUpload_commitFail_triggersEditDelete(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:      "edit-commit-fail",
		versionCode: 999,
		commitHandler: func(c testkit.Call) (int, string) {
			return 503, `{"error":{"code":503,"message":"service unavailable"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Upload: want error after commit failure, got nil")
	}

	sawDelete := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-commit-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after commit failure; calls = %v", apiCalls(rt))
	}
}

// TestUpload_commitFail_keepOnFailure_carriesEditID asserts the same
// DanglingEditError wrapping happens on a commit failure when
// KeepEditOnFailure is set.
func TestUpload_commitFail_keepOnFailure_carriesEditID(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		editID:      "edit-commit-keep",
		versionCode: 1000,
		commitHandler: func(c testkit.Call) (int, string) {
			return 503, `{"error":{"code":503,"message":"service unavailable"}}`
		},
		deleteHandler: func(c testkit.Call) (int, string) {
			t.Errorf("DELETE was called despite KeepEditOnFailure=true: %s", c.Path)
			return 204, ""
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:           "com.example.app",
		Track:             "internal",
		AABPath:           aab,
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Upload: want error after commit failure, got nil")
	}
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-commit-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-commit-keep", dangling.EditID)
	}
}

// TestUpload_insertEdit_403_returnsExit11Error asserts that a 403 from
// edits.insert (SA not invited on the app) surfaces as an error
// carrying ExitCode()=11: the canonical authorization-failure code
// per docs/DESIGN.md §9.
func TestUpload_insertEdit_403_returnsExit11Error(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		insertHandler: func(c testkit.Call) (int, string) {
			return 403, `{"error":{"code":403,"message":"forbidden"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Upload: want error from 403 insert, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 11 {
		t.Errorf("ExitCode() = %d, want 11", coder.ExitCode())
	}
}

// TestUpload_insertEdit_409_returnsExit60Error asserts that a 409 from
// edits.insert (a stale Edit is still open on the package) surfaces as
// an error carrying ExitCode()=60: the state-conflict code.
func TestUpload_insertEdit_409_returnsExit60Error(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		insertHandler: func(c testkit.Call) (int, string) {
			return 409, `{"error":{"code":409,"message":"another edit is in progress"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Upload: want error from 409 insert, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", coder.ExitCode())
	}
}

// TestUpload_withReleaseNotesDir_explicitLocales_skipsDetailsGet asserts
// the optimization from finding #8: when --release-notes-dir contains
// only explicit per-locale .txt files (no default.txt fallback), the
// orchestrator skips the edits.details.get round-trip: DefaultLanguage
// is not needed by notes.Load in that case.
func TestUpload_withReleaseNotesDir_explicitLocales_skipsDetailsGet(t *testing.T) {
	aab := writeFakeAAB(t)
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "en-US.txt"), []byte("English changelog"), 0644); err != nil {
		t.Fatalf("WriteFile en-US: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "fr-FR.txt"), []byte("Changelog français"), 0644); err != nil {
		t.Fatalf("WriteFile fr-FR: %v", err)
	}

	rt, transport := newPlay(playAPI{
		editID:      "edit-explicit",
		versionCode: 300,
		detailsHandler: func(c testkit.Call) (int, string) {
			t.Errorf("details.get was called for an explicit-only notes dir: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         aab,
		ReleaseNotesDir: notesDir,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// tracks.update body still carries both locales.
	body := string(trackUpdateReq(rt))
	for _, want := range []string{
		`"language":"en-US"`,
		`"language":"fr-FR"`,
		"English changelog",
		"Changelog français",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want substring %q", body, want)
		}
	}

	if result.DefaultLanguage != "" {
		t.Errorf("result.DefaultLanguage = %q, want empty (no details.get called)", result.DefaultLanguage)
	}
	if len(result.Locales) != 2 {
		t.Errorf("result.Locales = %v, want 2 entries (en-US, fr-FR)", result.Locales)
	}
}

// TestUpload_withReleaseNotesDir_defaultTxt_fetchesDetailsGet asserts
// the inverse: when the directory contains default.txt, the
// orchestrator DOES fetch DefaultLanguage so notes.Load can assign the
// fallback to the right locale.
func TestUpload_withReleaseNotesDir_defaultTxt_fetchesDetailsGet(t *testing.T) {
	aab := writeFakeAAB(t)
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "default.txt"), []byte("Fallback notes"), 0644); err != nil {
		t.Fatalf("WriteFile default: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "fr-FR.txt"), []byte("Notes en français"), 0644); err != nil {
		t.Fatalf("WriteFile fr-FR: %v", err)
	}

	rt, transport := newPlay(playAPI{
		editID:          "edit-defaulttxt",
		versionCode:     400,
		defaultLanguage: "en-US",
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         aab,
		ReleaseNotesDir: notesDir,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	sawDetails := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			sawDetails = true
			break
		}
	}
	if !sawDetails {
		t.Errorf("details.get not called despite default.txt being present; calls = %v", apiCalls(rt))
	}
	if result.DefaultLanguage != "en-US" {
		t.Errorf("result.DefaultLanguage = %q, want en-US", result.DefaultLanguage)
	}
}

// TestUpload_productionComplete_withoutConfirm_returnsExit3 asserts the
// --confirm guardrail: a production publish that would reach real
// users (Completed) without Confirm=true must refuse before any HTTP.
func TestUpload_productionComplete_withoutConfirm_returnsExit3(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:      "should-not-open",
		versionCode: 0,
		// Any HTTP call here would fail the test: a refused confirm
		// must short-circuit before insert.
		insertHandler: func(c testkit.Call) (int, string) {
			t.Errorf("insert was called despite missing --confirm: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		Status:  orchestrator.StatusCompleted,
		Confirm: false,
	})
	if err == nil {
		t.Fatal("Upload(production+Completed without Confirm): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 3 {
		t.Errorf("ExitCode() = %d, want 3 (safety flag required, #408)", coder.ExitCode())
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before --confirm guard, saw: %v", apiCalls(rt))
	}
}

// TestUpload_productionDraft_withoutConfirm_works asserts that the
// --confirm guard does NOT block production draft uploads (the safe
// path that does not affect real users).
func TestUpload_productionDraft_withoutConfirm_works(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		editID:             "edit-prod-draft",
		versionCode:        700,
		trackUpdateRawResp: `{"track":"production","releases":[{"status":"draft"}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		// Status unspecified → safe-default = draft on production
	})
	if err != nil {
		t.Fatalf("Upload(production safe-default): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

// TestUpload_dryRun_makesNoHTTPCalls asserts that --dry-run completes
// successfully and emits no HTTP calls. The returned Result describes
// the planned payload.
func TestUpload_dryRun_makesNoHTTPCalls(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID: "should-not-open",
		insertHandler: func(c testkit.Call) (int, string) {
			t.Errorf("HTTP call in dry-run mode: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Upload(dry-run): %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run made HTTP calls: %v", apiCalls(rt))
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed (internal track safe-default)", result.Status)
	}
	if result.ReleaseName != "(dry-run)" {
		t.Errorf("result.ReleaseName = %q, want (dry-run)", result.ReleaseName)
	}
}

// TestUpload_dryRun_productionSafeDefault asserts that dry-run also
// applies the ADR-0002 safe-default: production with no status flag
// previews as draft.
func TestUpload_dryRun_productionSafeDefault(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Upload(dry-run production): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

// writeFakeMapping creates a non-empty mapping.txt in t.TempDir(). The
// mock RoundTripper does not validate the bytes: bundles/mappings just
// need os.Open to succeed.
func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_withMapping_uploadsMappingInSameEdit asserts that supplying
// a MappingPath uploads the ProGuard mapping inside the SAME edit, keyed
// by the versionCode bundles.upload returned, between the bundle upload
// and the track update: one edit, one commit (#250).
func TestUpload_withMapping_uploadsMappingInSameEdit(t *testing.T) {
	aab := writeFakeAAB(t)
	mapping := writeFakeMapping(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		AABPath:     aab,
		MappingPath: mapping,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	calls := apiCalls(rt)
	if len(calls) != len(wantPaths) {
		t.Fatalf("got %d calls (%v), want %d", len(calls), calls, len(wantPaths))
	}
	for i, want := range wantPaths {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
	}
	if len(deobfReqBody(rt)) == 0 {
		t.Error("deobfuscationfiles.upload received an empty body; want the mapping bytes")
	}
	if !result.MappingUploaded {
		t.Error("result.MappingUploaded = false, want true after a --mapping upload")
	}
}

// TestUpload_dryRun_withMapping_validatesMappingFile asserts a dry-run
// with --mapping validates the mapping is readable (exit 20 when absent)
// and performs no HTTP when present.
func TestUpload_dryRun_withMapping_validatesMappingFile(t *testing.T) {
	aab := writeFakeAAB(t)

	// Missing mapping → dry-run validation failure (exit 20), no HTTP.
	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		AABPath:     aab,
		MappingPath: "/no/such/mapping.txt",
		DryRun:      true,
	})
	if err == nil {
		t.Fatal("dry-run returned nil error for a missing mapping")
	}
	if got := exitCode(err); got != 20 {
		t.Errorf("exit code = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt))
	}

	// Present mapping → dry-run succeeds with no HTTP.
	mapping := writeFakeMapping(t)
	rt2, transport2 := newPlay(playAPI{})
	hc2 := &http.Client{Transport: transport2}
	if _, err := orchestrator.Upload(context.Background(), hc2, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		AABPath:     aab,
		MappingPath: mapping,
		DryRun:      true,
	}); err != nil {
		t.Fatalf("dry-run with a present mapping: %v", err)
	}
	if touched(rt2) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt2))
	}
}

// exitCode extracts a gplay exit code from err via the Coder contract,
// defaulting to 1 when none is present.
func exitCode(err error) int {
	var c interface{ ExitCode() int }
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	return 1
}

// TestUpload_dryRun_mappingIsDirectory_exit20_noHTTP asserts that a
// dry-run `releases upload --mapping <dir>` rejects a non-regular mapping
// path as exit 20: parity with the live path (PR #264 review).
func TestUpload_dryRun_mappingIsDirectory_exit20_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	dir := t.TempDir()
	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		AABPath:     aab,
		MappingPath: dir,
		DryRun:      true,
	})
	if err == nil {
		t.Fatal("dry-run accepted a directory --mapping path")
	}
	if got := exitCode(err); got != 20 {
		t.Errorf("exit code = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt))
	}
}

// TestUpload_dryRun_aabIsDirectory_exit20_noHTTP asserts that a dry-run
// `releases upload` rejects a non-regular AAB path (a directory) as exit
// 20: parity with the live path (bundles.Upload), which cannot stream a
// directory. os.Stat succeeds on a directory, so without a regular-file
// guard dry-run would green-light what the live upload rejects as a
// transport error (exit 50).
func TestUpload_dryRun_aabIsDirectory_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: dir,
		DryRun:  true,
	})
	if err == nil {
		t.Fatal("dry-run accepted a directory AAB path")
	}
	if got := exitCode(err); got != 20 {
		t.Errorf("exit code = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt))
	}
}
