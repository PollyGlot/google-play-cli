// Package orchestrator_test exercises the upload orchestration end-to-end
// through a RoundTripper-mocked Google Play Developer API. Each test
// drives one behavior — the tracer bullet here is the canonical
// four-call sequence on a non-production track.
package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// playRT routes calls to the androidpublisher endpoints involved in an
// upload: edits.insert, edits.details.get, bundles.upload, tracks.update,
// edits.commit, and the cleanup edits.delete. Each endpoint has a default
// happy-path handler; tests override any specific handler to inject a
// failure or to assert on the request body.
type playRT struct {
	t                  *testing.T
	editID             string
	versionCode        int
	defaultLanguage    string // returned by the default details handler
	trackUpdateRawResp string

	// Optional handler overrides. nil → use the default happy-path
	// response for that endpoint.
	insertHandler  func(req *http.Request) (*http.Response, error)
	detailsHandler func(req *http.Request) (*http.Response, error)
	bundleHandler  func(req *http.Request) (*http.Response, error)
	trackHandler   func(req *http.Request) (*http.Response, error)
	commitHandler  func(req *http.Request) (*http.Response, error)
	deleteHandler  func(req *http.Request) (*http.Response, error)

	// Recorded for assertions.
	calls          []string
	trackUpdateReq []byte
}

func (p *playRT) RoundTrip(req *http.Request) (*http.Response, error) {
	p.calls = append(p.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		if p.insertHandler != nil {
			return p.insertHandler(req)
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, p.editID)), nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
		if p.detailsHandler != nil {
			return p.detailsHandler(req)
		}
		lang := p.defaultLanguage
		if lang == "" {
			lang = "en-US"
		}
		return jsonResp(200, fmt.Sprintf(`{"defaultLanguage":%q,"contactEmail":"x@example.com"}`, lang)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		if p.deleteHandler != nil {
			return p.deleteHandler(req)
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/bundles"):
		if p.bundleHandler != nil {
			return p.bundleHandler(req)
		}
		body := fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, p.versionCode)
		return jsonResp(200, body), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/tracks/"):
		if p.trackHandler != nil {
			return p.trackHandler(req)
		}
		body, _ := io.ReadAll(req.Body)
		p.trackUpdateReq = body
		resp := p.trackUpdateRawResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(200, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		if p.commitHandler != nil {
			return p.commitHandler(req)
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, p.editID)), nil
	}
	p.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// writeFakeAAB creates a non-empty file in t.TempDir() with a .aab
// extension. The mock RoundTripper does not validate the bytes — it just
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
	rt := &playRT{
		t:                  t,
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	hc := &http.Client{Transport: rt}

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

	// Canonical four-call sequence.
	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	if len(rt.calls) != len(wantPaths) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantPaths))
	}
	for i, want := range wantPaths {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// Track update payload carries status=completed, userFraction=1.0.
	body := string(rt.trackUpdateReq)
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
	rt := &playRT{
		t:                  t,
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"draft","versionCodes":["200"]}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		// Status: StatusUnspecified (default zero value) → safe-default applies.
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	body := string(rt.trackUpdateReq)
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
	rt := &playRT{
		t:                  t,
		editID:             "edit-prod-c",
		versionCode:        201,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "production",
		AABPath: aab,
		Status:  orchestrator.StatusCompleted,
		Confirm: true, // production publish gate (see ConfirmRequiredError)
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	body := string(rt.trackUpdateReq)
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
	rt := &playRT{
		t:                  t,
		editID:             "edit-prod-s",
		versionCode:        202,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:      "com.example.app",
		Track:        "production",
		AABPath:      aab,
		Status:       orchestrator.StatusInProgress,
		UserFraction: 0.05,
		Confirm:      true, // production publish gate (see ConfirmRequiredError)
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	body := string(rt.trackUpdateReq)
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
	rt := &playRT{
		t:      t,
		editID: "edit-fail",
		bundleHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(400, `{"error":{"code":400,"message":"malformed AAB"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
	})
	if err == nil {
		t.Fatal("Upload: want error after bundle failure, got nil")
	}

	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after bundle upload failure; calls = %v", rt.calls)
	}
}

// TestUpload_bundleUploadFail_keepOnFailure_doesNotDelete asserts the
// --keep-edit-on-failure opt-out: when KeepEditOnFailure=true, a mid-Edit
// failure must NOT trigger edits.delete, so the operator can inspect the
// open Edit ID and explicitly discard it later.
func TestUpload_bundleUploadFail_keepOnFailure_doesNotDelete(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t:      t,
		editID: "edit-keep",
		bundleHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(400, `{"error":{"code":400,"message":"malformed AAB"}}`), nil
		},
		deleteHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("DELETE was called despite KeepEditOnFailure=true: %s", req.URL.Path)
			return jsonResp(204, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:           "com.example.app",
		Track:             "internal",
		AABPath:           aab,
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Upload: want error after bundle failure, got nil")
	}

	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			t.Errorf("KeepEditOnFailure=true but saw DELETE in calls: %v", rt.calls)
		}
	}
}

// TestUpload_insertEdit_403_returnsExit11Error asserts that a 403 from
// edits.insert (SA not invited on the app) surfaces as an error
// carrying ExitCode()=11 — the canonical authorization-failure code
// per docs/DESIGN.md §9.
func TestUpload_insertEdit_403_returnsExit11Error(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t: t,
		insertHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(403, `{"error":{"code":403,"message":"forbidden"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

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
// an error carrying ExitCode()=60 — the state-conflict code.
func TestUpload_insertEdit_409_returnsExit60Error(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t: t,
		insertHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(409, `{"error":{"code":409,"message":"another edit is in progress"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

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

// TestUpload_withReleaseNotesDir_attachesPerLocaleNotes is the end-to-end
// integration of the notes loader: when --release-notes-dir is set, the
// orchestrator fetches the app's default language via edits.details.get,
// loads the per-locale .txt files, and attaches them as releaseNotes on
// the tracks.update payload.
func TestUpload_withReleaseNotesDir_attachesPerLocaleNotes(t *testing.T) {
	aab := writeFakeAAB(t)
	notesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(notesDir, "en-US.txt"), []byte("English changelog"), 0644); err != nil {
		t.Fatalf("WriteFile en-US: %v", err)
	}
	if err := os.WriteFile(filepath.Join(notesDir, "fr-FR.txt"), []byte("Changelog français"), 0644); err != nil {
		t.Fatalf("WriteFile fr-FR: %v", err)
	}

	rt := &playRT{
		t:               t,
		editID:          "edit-notes",
		versionCode:     300,
		defaultLanguage: "en-US",
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:         "com.example.app",
		Track:           "internal",
		AABPath:         aab,
		ReleaseNotesDir: notesDir,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// details.get must have been called.
	sawDetails := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			sawDetails = true
			break
		}
	}
	if !sawDetails {
		t.Errorf("details.get not called; calls = %v", rt.calls)
	}

	// tracks.update body carries both locales and their text.
	body := string(rt.trackUpdateReq)
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

	if result.DefaultLanguage != "en-US" {
		t.Errorf("result.DefaultLanguage = %q, want en-US", result.DefaultLanguage)
	}
	if len(result.Locales) != 2 {
		t.Errorf("result.Locales = %v, want 2 entries (en-US, fr-FR)", result.Locales)
	}
}

// TestUpload_productionComplete_withoutConfirm_returnsExit2 asserts the
// --confirm guardrail: a production publish that would reach real
// users (Completed) without Confirm=true must refuse before any HTTP.
func TestUpload_productionComplete_withoutConfirm_returnsExit2(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t:           t,
		editID:      "should-not-open",
		versionCode: 0,
		// Any HTTP call here would fail the test — a refused confirm
		// must short-circuit before insert.
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("insert was called despite missing --confirm: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

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
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before --confirm guard, saw: %v", rt.calls)
	}
}

// TestUpload_productionDraft_withoutConfirm_works asserts that the
// --confirm guard does NOT block production draft uploads (the safe
// path that does not affect real users).
func TestUpload_productionDraft_withoutConfirm_works(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{
		t:                  t,
		editID:             "edit-prod-draft",
		versionCode:        700,
		trackUpdateRawResp: `{"track":"production","releases":[{"status":"draft"}]}`,
	}
	hc := &http.Client{Transport: rt}

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
	rt := &playRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("HTTP call in dry-run mode: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: aab,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Upload(dry-run): %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run made HTTP calls: %v", rt.calls)
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed (internal track safe-default)", result.Status)
	}
	if result.ReleaseName != "(dry-run)" {
		t.Errorf("result.ReleaseName = %q, want (dry-run)", result.ReleaseName)
	}
}

// TestUpload_dryRun_productionSafeDefault asserts that dry-run also
// applies the ADR-0002 safe-default — production with no status flag
// previews as draft.
func TestUpload_dryRun_productionSafeDefault(t *testing.T) {
	aab := writeFakeAAB(t)
	rt := &playRT{t: t}
	hc := &http.Client{Transport: rt}

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
