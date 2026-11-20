package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type handler func(c testkit.Call) (int, string)

type playAPI struct {
	editID             string
	versionCode        int
	defaultLanguage    string
	trackUpdateRawResp string
	versionCodes       []int
	hostedVersionCode  int

	insertHandler  handler
	detailsHandler handler
	bundleHandler  handler
	deobfHandler   handler
	trackHandler   handler
	commitHandler  handler
	deleteHandler  handler
}

func newPlay(a playAPI) (*testkit.Fake, http.RoundTripper) {
	override := func(h handler, c testkit.Call) (int, string, bool) {
		status, body := h(c)
		return status, body, true
	}
	var (
		mu      sync.Mutex
		uploads int
	)
	nextCode := func() int {
		mu.Lock()
		defer mu.Unlock()
		code := a.versionCode
		if uploads < len(a.versionCodes) {
			code = a.versionCodes[uploads]
		}
		uploads++
		return code
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
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/apks/externallyHosted"):
			return http.StatusOK, fmt.Sprintf(`{"externallyHostedApk":{"packageName":"com.example.app","versionCode":%d}}`, a.hostedVersionCode), true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/bundles"):
			if a.bundleHandler != nil {
				return override(a.bundleHandler, c)
			}
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/bundles"):
			return http.StatusOK, fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, nextCode()), true
		case c.Method == http.MethodPost && strings.Contains(c.Path, "/deobfuscationFiles/"):
			if a.deobfHandler != nil {
				return override(a.deobfHandler, c)
			}
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
		if status == http.StatusOK && c.Method == http.MethodPost &&
			(strings.Contains(c.Path, "/bundles") || strings.Contains(c.Path, "/deobfuscationFiles/")) {
			return "https://" + c.Host + c.Path + "?upload_id=session-" + a.editID
		}
		return ""
	}))
	return f, f
}

func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

func lastBody(f *testkit.Fake, method, fragment string) []byte {
	var body []byte
	for _, c := range f.Calls() {
		if c.Method == method && strings.Contains(c.Path, fragment) {
			body = c.Body
		}
	}
	return body
}

func trackUpdateReq(f *testkit.Fake) []byte { return lastBody(f, http.MethodPut, "/tracks/") }

func deobfReqBody(f *testkit.Fake) []byte {
	return lastBody(f, http.MethodPut, "/deobfuscationFiles/")
}

func writeFakeAAB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.aab")
	if err := os.WriteFile(p, []byte("fake-aab-content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestUpload_internalTrack_happyPath_setsCompletedRelease(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

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

	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("tracks.update body = %s, want no userFraction on a completed release", body)
	}

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

func TestUpload_productionTrack_safeDefault_sendsDraftStatus(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"draft","versionCodes":["200"]}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"draft"`) {
		t.Errorf("production safe-default: body = %s, want status=draft", body)
	}
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

func TestUpload_productionTrack_explicitCompleted_sendsCompletedFull(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod-c",
		versionCode:        201,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
		State:     orchestrator.Completed(),
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("body = %s, want status=completed", body)
	}
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("body = %s, want no userFraction on a completed release", body)
	}
	if result.Status != "completed" || result.UserFraction != 0 {
		t.Errorf("result = (%q, %v), want (completed, no fraction)", result.Status, result.UserFraction)
	}
}

func TestUpload_productionTrack_explicitStaged_sendsInProgressWithFraction(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:             "edit-prod-s",
		versionCode:        202,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
		State:     staged(t, 0.05),
		Confirm:   true,
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
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
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
		Artifacts:         []orchestrator.Artifact{{Path: aab}},
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

	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError carrying the Edit ID", err, err)
	}
	if dangling.EditID != "edit-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-keep", dangling.EditID)
	}
}

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
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
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
		Artifacts:         []orchestrator.Artifact{{Path: aab}},
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

func TestUpload_insertEdit_403_returnsExit11Error(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		insertHandler: func(c testkit.Call) (int, string) {
			return 403, `{"error":{"code":403,"message":"forbidden"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
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

func TestUpload_insertEdit_409_returnsExit60Error(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		insertHandler: func(c testkit.Call) (int, string) {
			return 409, `{"error":{"code":409,"message":"another edit is in progress"}}`
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
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
		Artifacts:       []orchestrator.Artifact{{Path: aab}},
		ReleaseNotesDir: notesDir,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

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
		Artifacts:       []orchestrator.Artifact{{Path: aab}},
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

func TestUpload_productionComplete_withoutConfirm_returnsExit3(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newPlay(playAPI{
		editID:      "should-not-open",
		versionCode: 0,
		insertHandler: func(c testkit.Call) (int, string) {
			t.Errorf("insert was called despite missing --confirm: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: transport}

	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
		State:     orchestrator.Completed(),
		Confirm:   false,
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

func TestUpload_productionDraft_withoutConfirm_works(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{
		editID:             "edit-prod-draft",
		versionCode:        700,
		trackUpdateRawResp: `{"track":"production","releases":[{"status":"draft"}]}`,
	})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
	})
	if err != nil {
		t.Fatalf("Upload(production safe-default): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

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
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
		DryRun:    true,
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

func TestUpload_dryRun_productionSafeDefault(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}

	result, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []orchestrator.Artifact{{Path: aab}},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Upload(dry-run production): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

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
		Artifacts:   []orchestrator.Artifact{{Path: aab}},
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

func TestUpload_dryRun_withMapping_validatesMappingFile(t *testing.T) {
	aab := writeFakeAAB(t)

	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		Artifacts:   []orchestrator.Artifact{{Path: aab}},
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

	mapping := writeFakeMapping(t)
	rt2, transport2 := newPlay(playAPI{})
	hc2 := &http.Client{Transport: transport2}
	if _, err := orchestrator.Upload(context.Background(), hc2, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		Artifacts:   []orchestrator.Artifact{{Path: aab}},
		MappingPath: mapping,
		DryRun:      true,
	}); err != nil {
		t.Fatalf("dry-run with a present mapping: %v", err)
	}
	if touched(rt2) {
		t.Errorf("dry-run hit the network: %v", apiCalls(rt2))
	}
}

func exitCode(err error) int {
	var c interface{ ExitCode() int }
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	return 1
}

func TestUpload_dryRun_mappingIsDirectory_exit20_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	dir := t.TempDir()
	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		Artifacts:   []orchestrator.Artifact{{Path: aab}},
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

func TestUpload_dryRun_aabIsDirectory_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir()
	rt, transport := newPlay(playAPI{})
	hc := &http.Client{Transport: transport}
	_, err := orchestrator.Upload(context.Background(), hc, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: dir}},
		DryRun:    true,
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
