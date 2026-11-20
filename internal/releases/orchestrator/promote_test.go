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

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type promoteAPI struct {
	editID             string
	sourceTrackGetResp string
	destTrack          string
	destTrackGetResp   string
	trackUpdateRawResp string
	defaultLanguage    string

	insertHandler  handler
	getHandler     handler
	detailsHandler handler
	updateHandler  handler
	commitHandler  handler
	deleteHandler  handler
}

func newPromoteFake(a promoteAPI) *testkit.Fake {
	override := func(h handler, c testkit.Call) (int, string, bool) {
		status, body := h(c)
		return status, body, true
	}
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			if a.insertHandler != nil {
				return override(a.insertHandler, c)
			}
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			if a.deleteHandler != nil {
				return override(a.deleteHandler, c)
			}
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/details"):
			if a.detailsHandler != nil {
				return override(a.detailsHandler, c)
			}
			lang := a.defaultLanguage
			if lang == "" {
				lang = "en-US"
			}
			return http.StatusOK, fmt.Sprintf(`{"defaultLanguage":%q,"contactEmail":"x@example.com"}`, lang), true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			if a.getHandler != nil {
				return override(a.getHandler, c)
			}
			if a.destTrack != "" && strings.HasSuffix(c.Path, "/tracks/"+a.destTrack) {
				return http.StatusOK, a.destTrackGetResp, true
			}
			return http.StatusOK, a.sourceTrackGetResp, true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/"):
			if a.updateHandler != nil {
				return override(a.updateHandler, c)
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
	})
}

func TestPromote_internalToBeta_happyPath_copiesVersionCode(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-promote/tracks/internal",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-promote/tracks/beta",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-promote/tracks/beta",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-promote:commit",
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
	if !strings.Contains(body, `"versionCodes":["142"]`) {
		t.Errorf("tracks.update body = %s, want versionCodes=[\"142\"]", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("tracks.update body = %s, want no userFraction on a completed release", body)
	}

	if result.VersionCode != 142 {
		t.Errorf("VersionCode = %d, want 142", result.VersionCode)
	}
	if result.Track != "beta" {
		t.Errorf("Track = %q, want beta", result.Track)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

func TestPromote_internalToProduction_safeDefault_sendsDraftStatus(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote-prod",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"200","status":"completed","versionCodes":["200"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"draft","versionCodes":["200"]}]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "production",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
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

func TestPromote_toProduction_explicitStaged_sendsInProgressWithFraction(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote-staged",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"301","status":"completed","versionCodes":["301"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"301","status":"inProgress","versionCodes":["301"],"userFraction":0.05}]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		State:     staged(t, 0.05),
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
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

func TestPromote_ambiguousSource_noDisambiguator_returnsExit60Error(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-ambiguous",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			t.Errorf("tracks.update was called despite ambiguous source: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "production",
		ToTrack:   "internal",
	})
	if err == nil {
		t.Fatal("Promote(ambiguous source without disambiguator): want error, got nil")
	}

	var ambig *orchestrator.AmbiguousReleaseError
	if !errors.As(err, &ambig) {
		t.Fatalf("err = %v (%T), want *orchestrator.AmbiguousReleaseError", err, err)
	}
	if ambig.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", ambig.ExitCode())
	}
	msg := ambig.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "501") {
		t.Errorf("AmbiguousReleaseError.Error() = %q, want both candidate versionCodes (500, 501)", msg)
	}

	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "PUT ") {
			t.Errorf("destination tracks.update should not be called on ambiguous source; calls = %v", apiCalls(rt))
		}
	}
}

func TestPromote_ambiguousSource_versionCodeDisambiguates(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-disambig",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"501","status":"completed","versionCodes":["501"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 501,
	})
	if err != nil {
		t.Fatalf("Promote with --version-code 501: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("VersionCode = %d, want 501", result.VersionCode)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"versionCodes":["501"]`) {
		t.Errorf("body = %s, want versionCodes=[\"501\"] (the picked release)", body)
	}
	if strings.Contains(body, `"versionCodes":["500"]`) {
		t.Errorf("body = %s, should NOT include the non-picked release 500", body)
	}
}

func TestPromote_ambiguousSource_releaseNameDisambiguates(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-disambig-name",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"v2.0-rc1","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"v2.0-rc2","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		ReleaseName: "v2.0-rc2",
	})
	if err != nil {
		t.Fatalf("Promote with --release-name v2.0-rc2: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("VersionCode = %d, want 501 (the rc2 release)", result.VersionCode)
	}
}

func TestPromote_versionCodeNotFound_returnsExit60Error(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-not-found",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"500","status":"completed","versionCodes":["500"],"userFraction":1.0}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			t.Errorf("tracks.update was called despite missing --version-code match: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 999,
	})
	if err == nil {
		t.Fatal("Promote(unknown --version-code): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", coder.ExitCode())
	}
}

func TestPromote_ambiguousAfterReleaseName_hintSaysFilterAlreadyApplied(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-ambig-after-filter",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"v2.0","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"v2.0","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		ReleaseName: "v2.0",
	})
	if err == nil {
		t.Fatal("Promote(ambiguous after filter): want error, got nil")
	}
	var ambig *orchestrator.AmbiguousReleaseError
	if !errors.As(err, &ambig) {
		t.Fatalf("err = %v (%T), want *AmbiguousReleaseError", err, err)
	}
	if !ambig.FilterApplied {
		t.Errorf("FilterApplied = false, want true (caller passed --release-name)")
	}
	msg := ambig.Error()
	if !strings.Contains(msg, "tighten") && !strings.Contains(msg, "combine") {
		t.Errorf("err = %q, want hint to mention tightening/combining the existing filter, not repeating it", msg)
	}
}

func TestPromote_noMatchingRelease_listsBothCriteria(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-both-filters",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"v1.9-hotfix","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 142,
		ReleaseName: "v2.0",
	})
	if err == nil {
		t.Fatal("Promote(both filters, neither matches together): want error, got nil")
	}
	var nme *orchestrator.NoMatchingReleaseError
	if !errors.As(err, &nme) {
		t.Fatalf("err = %v (%T), want *NoMatchingReleaseError", err, err)
	}
	msg := nme.Error()
	if !strings.Contains(msg, "--version-code=142") {
		t.Errorf("err = %q, want --version-code=142 mentioned", msg)
	}
	if !strings.Contains(msg, "--release-name=v2.0") {
		t.Errorf("err = %q, want --release-name=v2.0 mentioned", msg)
	}
}

func TestPromote_releaseNotesCarryOver_verbatim(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-notes-carry",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"600","status":"completed","versionCodes":["600"],"userFraction":1.0,` +
			`"releaseNotes":[` +
			`{"language":"en-US","text":"English notes from source"},` +
			`{"language":"fr-FR","text":"Notes en français"}` +
			`]}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(trackUpdateReq(rt))
	for _, want := range []string{
		`"language":"en-US"`,
		`"language":"fr-FR"`,
		"English notes from source",
		"Notes en français",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want substring %q (carry-over)", body, want)
		}
	}

	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			t.Errorf("edits.details.get called despite verbatim carry-over: %v", apiCalls(rt))
		}
	}

	if len(result.Locales) != 2 {
		t.Errorf("result.Locales = %v, want 2 entries (en-US, fr-FR)", result.Locales)
	}
}

func TestPromote_releaseNotesCarryOver_noSourceNotes(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-no-notes",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"700","status":"completed","versionCodes":["700"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(trackUpdateReq(rt))
	if strings.Contains(body, `"releaseNotes"`) {
		t.Errorf("body = %s, should NOT include empty releaseNotes when source has none", body)
	}
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			t.Errorf("edits.details.get called despite no notes: %v", apiCalls(rt))
		}
	}
}

func TestPromote_releaseNotesFlag_overridesCarryOver(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "edit-notes-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"800","status":"completed","versionCodes":["800"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"OLD source notes"}]` +
			`}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:      "com.example.app",
		FromTrack:    "internal",
		ToTrack:      "beta",
		ReleaseNotes: "Fresh notes for the promotion",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, "Fresh notes for the promotion") {
		t.Errorf("body = %s, want the --release-notes text", body)
	}
	if strings.Contains(body, "OLD source notes") {
		t.Errorf("body = %s, must NOT carry over source notes when --release-notes is set", body)
	}
	if !strings.Contains(body, `"language":"en-US"`) {
		t.Errorf("body = %s, want notes attached to the app DefaultLanguage (en-US)", body)
	}

	sawDetails := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			sawDetails = true
			break
		}
	}
	if !sawDetails {
		t.Errorf("--release-notes set but no details.get call; calls = %v", apiCalls(rt))
	}
	if result.DefaultLanguage != "en-US" {
		t.Errorf("result.DefaultLanguage = %q, want en-US", result.DefaultLanguage)
	}
}

func TestPromote_releaseNotesDir_emptyOrTypod_refusesRatherThanDropSource(t *testing.T) {
	emptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyDir, "README.md"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := newPromoteFake(promoteAPI{
		editID: "edit-empty-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"800","status":"completed","versionCodes":["800"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"source notes"}]` +
			`}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			t.Errorf("tracks.update was called despite empty override dropping source notes: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: emptyDir,
	})
	if err == nil {
		t.Fatal("Promote(empty override dir + notes on source): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if !strings.Contains(err.Error(), "en-US") {
		t.Errorf("err = %q, want it to name the dropped locale 'en-US'", err.Error())
	}
}

func TestPromote_releaseNotesDir_partialOverride_refusesRatherThanDropOtherLocales(t *testing.T) {
	partialDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(partialDir, "fr-FR.txt"), []byte("notes en français"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := newPromoteFake(promoteAPI{
		editID: "edit-partial-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"801","status":"completed","versionCodes":["801"],"userFraction":1.0,` +
			`"releaseNotes":[` +
			`{"language":"en-US","text":"English notes"},` +
			`{"language":"de-DE","text":"German notes"},` +
			`{"language":"fr-FR","text":"old French notes"}` +
			`]}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			t.Errorf("tracks.update was called despite partial override dropping source locales: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: partialDir,
	})
	if err == nil {
		t.Fatal("Promote(partial override + multi-locale source): want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "en-US") || !strings.Contains(msg, "de-DE") {
		t.Errorf("err = %q, want it to name dropped locales en-US and de-DE", msg)
	}
}

func TestPromote_releaseNotesDir_coversAllSourceLocales_succeeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Fresh English notes"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := newPromoteFake(promoteAPI{
		editID: "edit-full-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"802","status":"completed","versionCodes":["802"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"OLD"}]` +
			`}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: dir,
	})
	if err != nil {
		t.Fatalf("Promote(override covers all source locales): %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, "Fresh English notes") {
		t.Errorf("body = %s, want the override text", body)
	}
}

func TestPromote_productionComplete_withoutConfirm_returnsExit3(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "should-not-open",
		insertHandler: func(c testkit.Call) (int, string) {
			t.Errorf("insert was called despite missing --confirm: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		State:     orchestrator.Completed(),
		Confirm:   false,
	})
	if err == nil {
		t.Fatal("Promote(production+Completed without Confirm): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 3 {
		t.Errorf("ExitCode() = %d, want 3 (safety flag required, #408)", coder.ExitCode())
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before --confirm guard, saw: %v", apiCalls(rt))
	}
}

func TestPromote_productionDraft_withoutConfirm_works(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-prod-draft",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"900","status":"completed","versionCodes":["900"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"900","status":"draft","versionCodes":["900"]}]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
	})
	if err != nil {
		t.Fatalf("Promote(production safe-default): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

func TestPromote_trackUpdateFail_triggersEditDelete(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote-fail",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"950","status":"completed","versionCodes":["950"],"userFraction":1.0}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			return 503, `{"error":{"code":503,"message":"service unavailable"}}`
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err == nil {
		t.Fatal("Promote: want error after track update failure, got nil")
	}

	sawDelete := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-promote-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after track update failure; calls = %v", apiCalls(rt))
	}
}

func TestPromote_validateOpts_libraryCallers(t *testing.T) {
	cases := []struct {
		name string
		opts orchestrator.PromoteOpts
	}{
		{
			name: "missing FromTrack",
			opts: orchestrator.PromoteOpts{Package: "com.example.app", ToTrack: "beta"},
		},
		{
			name: "missing ToTrack",
			opts: orchestrator.PromoteOpts{Package: "com.example.app", FromTrack: "internal"},
		},
		{
			name: "ReleaseNotes and ReleaseNotesDir both set",
			opts: orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
				ReleaseNotes: "x", ReleaseNotesDir: "/tmp/notes",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newPromoteFake(promoteAPI{
				editID: "should-not-open",
				insertHandler: func(c testkit.Call) (int, string) {
					t.Errorf("insert was called despite invalid opts: %s", c.Path)
					return 500, ""
				},
			})
			hc := &http.Client{Transport: rt}

			_, err := orchestrator.Promote(context.Background(), hc, tc.opts)
			if err == nil {
				t.Fatal("Promote(invalid opts): want error, got nil")
			}
			var coder interface{ ExitCode() int }
			if !errors.As(err, &coder) {
				t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
			}
			if coder.ExitCode() != 2 {
				t.Errorf("ExitCode() = %d, want 2 (InvalidOptsError)", coder.ExitCode())
			}
			if touched(rt) {
				t.Errorf("expected zero HTTP calls before validation error, saw: %v", apiCalls(rt))
			}
		})
	}
}

func TestPromote_dryRun_makesNoHTTPCalls(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID: "should-not-open",
		insertHandler: func(c testkit.Call) (int, string) {
			t.Errorf("HTTP call in dry-run mode: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run): %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run made HTTP calls: %v", apiCalls(rt))
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed (non-production safe-default preview)", result.Status)
	}
	if result.ReleaseName != "(dry-run)" {
		t.Errorf("result.ReleaseName = %q, want (dry-run)", result.ReleaseName)
	}
	if result.Track != "beta" {
		t.Errorf("result.Track = %q, want beta", result.Track)
	}
}

func TestPromote_dryRun_productionSafeDefault(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run production): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

func TestPromote_dryRun_productionCompleteNoConfirm_previewsWithoutGate(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		State:     orchestrator.Completed(),
		Confirm:   false,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run production complete, no confirm): %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed", result.Status)
	}
	if result.UserFraction != 0 {
		t.Errorf("result.UserFraction = %v, want no fraction on a completed release", result.UserFraction)
	}
}

func TestPromote_dryRun_invalidOptsStillCaught(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package: "com.example.app",
		ToTrack: "beta",
		DryRun:  true,
	})
	if err == nil {
		t.Fatal("Promote(dry-run with missing FromTrack): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

func TestPromote_sourceReleaseEmptyVersionCodes_returnsExit60Error(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-no-codes",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"v2.0-draft","status":"draft"}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			t.Errorf("tracks.update was called despite empty source versionCodes: %s", c.Path)
			return 500, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err == nil {
		t.Fatal("Promote(source release with no versionCodes): want typed error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", coder.ExitCode())
	}

	sawDelete := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-no-codes") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after empty-versionCodes error; calls = %v", apiCalls(rt))
	}
}

func TestPromote_trackUpdateFail_keepOnFailure_doesNotDelete(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote-keep",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"951","status":"completed","versionCodes":["951"],"userFraction":1.0}]}`,
		updateHandler: func(c testkit.Call) (int, string) {
			return 503, `{"error":{"code":503,"message":"service unavailable"}}`
		},
		deleteHandler: func(c testkit.Call) (int, string) {
			t.Errorf("DELETE was called despite KeepEditOnFailure=true: %s", c.Path)
			return 204, ""
		},
	})
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:           "com.example.app",
		FromTrack:         "internal",
		ToTrack:           "beta",
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Promote: want error after track update failure, got nil")
	}
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-promote-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-promote-keep", dangling.EditID)
	}
}
