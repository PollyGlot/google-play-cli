// Package orchestrator_test exercises the rollout state-machine verbs
// (Rollout / Halt / Resume / Complete) through the same RoundTripper-mocked
// Google Play Developer API used by the upload and promote suites. Each
// verb mutates the status / userFraction of the latest release on a track
// inside one Edit: edits.insert → tracks.get → tracks.update → edits.commit.
//
// These tests deliberately do NOT reuse promote's pickSourceRelease — the
// rollout family carries its own picker (pickTargetRelease) with its own
// not-found semantics (exit 30, vs promote's exit 60), so the suites stay
// independent.
package orchestrator_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// stateRT reuses promoteRT's routing (insert → tracks.get → tracks.update →
// commit, plus cleanup delete) since the state-machine call sequence is
// identical to a promote that targets a single track. sourceTrackGetResp
// stands in for "the track's current state" returned by tracks.get.
type stateRT = promoteRT

// wantStateSequence is the canonical four-call sequence every state verb
// issues against a single track.
func wantStateSequence(pkg, editID, track string) []string {
	base := "/androidpublisher/v3/applications/" + pkg + "/edits"
	return []string{
		"POST " + base,
		"GET " + base + "/" + editID + "/tracks/" + track,
		"PUT " + base + "/" + editID + "/tracks/" + track,
		"POST " + base + "/" + editID + ":commit",
	}
}

func assertSequence(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d calls (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRollout_setsInProgressAtTargetFraction is AC1: rollout --to 0.05
// ramps the latest release to userFraction 0.05 and status inProgress.
func TestRollout_setsInProgressAtTargetFraction(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-rollout",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.05}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
		Package:      "com.example.app",
		Track:        "production",
		UserFraction: 0.05,
		Confirm:      true,
	})
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}

	assertSequence(t, rt.calls, wantStateSequence("com.example.app", "edit-rollout", "production"))

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"inProgress"`) {
		t.Errorf("body = %s, want status=inProgress", body)
	}
	if !strings.Contains(body, `"userFraction":0.05`) {
		t.Errorf("body = %s, want userFraction=0.05", body)
	}
	if !strings.Contains(body, `"versionCodes":["142"]`) {
		t.Errorf("body = %s, want versionCodes preserved", body)
	}
	if result.Status != "inProgress" || result.UserFraction != 0.05 {
		t.Errorf("result = (%q, %v), want (inProgress, 0.05)", result.Status, result.UserFraction)
	}
	if result.VersionCode != 142 {
		t.Errorf("result.VersionCode = %d, want 142", result.VersionCode)
	}
}

// TestHalt_setsHaltedPreservesFraction is AC2: halt freezes the rollout —
// status becomes halted, userFraction is preserved as-is.
func TestHalt_setsHaltedPreservesFraction(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-halt",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"200","status":"inProgress","versionCodes":["200"],"userFraction":0.2}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"halted","versionCodes":["200"],"userFraction":0.2}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
	})
	if err != nil {
		t.Fatalf("Halt: %v", err)
	}

	assertSequence(t, rt.calls, wantStateSequence("com.example.app", "edit-halt", "production"))

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"halted"`) {
		t.Errorf("body = %s, want status=halted", body)
	}
	if !strings.Contains(body, `"userFraction":0.2`) {
		t.Errorf("body = %s, want userFraction preserved at 0.2", body)
	}
	if result.Status != "halted" || result.UserFraction != 0.2 {
		t.Errorf("result = (%q, %v), want (halted, 0.2)", result.Status, result.UserFraction)
	}
}

// TestResume_setsInProgressPreservesFraction is AC3: resume flips a halted
// release back to inProgress without touching the (preserved) fraction.
func TestResume_setsInProgressPreservesFraction(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-resume",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"300","status":"halted","versionCodes":["300"],"userFraction":0.2}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"300","status":"inProgress","versionCodes":["300"],"userFraction":0.2}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Resume(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"inProgress"`) {
		t.Errorf("body = %s, want status=inProgress", body)
	}
	if !strings.Contains(body, `"userFraction":0.2`) {
		t.Errorf("body = %s, want userFraction preserved at 0.2", body)
	}
	if result.Status != "inProgress" || result.UserFraction != 0.2 {
		t.Errorf("result = (%q, %v), want (inProgress, 0.2)", result.Status, result.UserFraction)
	}
}

// TestComplete_rampsToFullRollout is AC4: complete ramps userFraction to
// 1.0 and sets status completed.
func TestComplete_rampsToFullRollout(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-complete",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"400","status":"inProgress","versionCodes":["400"],"userFraction":0.2}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"400","status":"completed","versionCodes":["400"],"userFraction":1.0}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
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

// TestState_preservesReleaseNotes guards against silent data loss: a state
// transition PUTs the whole release back, so the current release's
// releaseNotes (and versionCodes) MUST be carried over verbatim — dropping
// them would wipe the track's notes on the next commit.
func TestState_preservesReleaseNotes(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-notes",
		sourceTrackGetResp: `{"track":"beta","releases":[{` +
			`"name":"500","status":"inProgress","versionCodes":["500"],"userFraction":0.1,` +
			`"releaseNotes":[` +
			`{"language":"en-US","text":"English notes"},` +
			`{"language":"fr-FR","text":"Notes en français"}` +
			`]}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "beta",
	})
	if err != nil {
		t.Fatalf("Halt: %v", err)
	}

	body := string(rt.trackUpdateReq)
	for _, want := range []string{
		`"language":"en-US"`,
		`"language":"fr-FR"`,
		"English notes",
		"Notes en français",
		`"versionCodes":["500"]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want substring %q preserved through the transition", body, want)
		}
	}
}

// TestState_ambiguousTrack_returnsExit60 is AC5: two coexisting releases
// (inProgress + halted) with no disambiguator must refuse with exit 60
// and must NOT write to the track.
func TestState_ambiguousTrack_returnsExit60(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-ambiguous",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.1},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.5}` +
			`]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update called despite ambiguous target: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
	})
	if err == nil {
		t.Fatal("Halt(ambiguous track): want error, got nil")
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
		t.Errorf("error = %q, want both candidate versionCodes (500, 501)", msg)
	}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "PUT ") {
			t.Errorf("tracks.update must not run on ambiguous target; calls = %v", rt.calls)
		}
	}
}

// TestState_versionCodeDisambiguates asserts --version-code selects the
// matching release out of multiple coexisting ones AND mutates only it.
func TestState_versionCodeDisambiguates(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-disambig",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.1},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.5}` +
			`]}`,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package:     "com.example.app",
		Track:       "production",
		VersionCode: 501,
	})
	if err != nil {
		t.Fatalf("Halt with --version-code 501: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("result.VersionCode = %d, want 501", result.VersionCode)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"versionCodes":["501"]`) {
		t.Errorf("body = %s, want the picked release 501", body)
	}
	// 501 was the only inProgress release; halting it must leave no
	// inProgress in the body (it was mutated, not some other release).
	if strings.Contains(body, `"inProgress"`) {
		t.Errorf("body = %s, want release 501 flipped away from inProgress", body)
	}
}

// TestState_coexistingReleases_preservedOnUpdate is the regression guard for
// the data-loss bug: tracks.update REPLACES the whole releases array, so
// acting on one release of a multi-release track must re-send the others
// unchanged. Without this, halting/completing one release silently deletes
// the coexisting one.
func TestState_coexistingReleases_preservedOnUpdate(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-coexist",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.1},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.5}` +
			`]}`,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package:     "com.example.app",
		Track:       "production",
		VersionCode: 501,
		Confirm:     true,
	})
	if err != nil {
		t.Fatalf("Complete --version-code 501: %v", err)
	}

	body := string(rt.trackUpdateReq)
	// The untouched release 500 must survive verbatim.
	if !strings.Contains(body, `"versionCodes":["500"]`) {
		t.Errorf("body = %s, coexisting release 500 was dropped (data loss)", body)
	}
	if !strings.Contains(body, `"userFraction":0.1`) {
		t.Errorf("body = %s, release 500's fraction 0.1 was not preserved", body)
	}
	// 501 ramped to completed/1.0.
	if !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("body = %s, want release 501 completed at 1.0", body)
	}
	// 500 was halted and must stay halted.
	if !strings.Contains(body, `"status":"halted"`) {
		t.Errorf("body = %s, want release 500 left halted", body)
	}
}

// TestState_preservesUnmodeledFields asserts that fields gplay does not model
// on the Release struct (here inAppUpdatePriority, and a nested
// countryTargeting) survive a transition — the raw-JSON patch must not strip
// them, since tracks.update would otherwise wipe configuration set elsewhere.
func TestState_preservesUnmodeledFields(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-unmodeled",
		sourceTrackGetResp: `{"track":"production","releases":[{` +
			`"name":"600","status":"inProgress","versionCodes":["600"],"userFraction":0.2,` +
			`"inAppUpdatePriority":4,` +
			`"countryTargeting":{"countries":["US","FR"],"includeRestOfWorld":false}` +
			`}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
	})
	if err != nil {
		t.Fatalf("Halt: %v", err)
	}

	body := string(rt.trackUpdateReq)
	for _, want := range []string{
		`"inAppUpdatePriority":4`,
		`"countryTargeting"`,
		`"US"`,
		`"FR"`,
		`"includeRestOfWorld":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want unmodeled field %q preserved through the transition", body, want)
		}
	}
	if !strings.Contains(body, `"status":"halted"`) {
		t.Errorf("body = %s, want status flipped to halted", body)
	}
}

// TestState_releaseNameDisambiguates asserts --release-name picks by the
// release's Name field.
func TestState_releaseNameDisambiguates(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-disambig-name",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"v2.0-rc1","status":"halted","versionCodes":["500"],"userFraction":0.1},` +
			`{"name":"v2.0-rc2","status":"inProgress","versionCodes":["501"],"userFraction":0.5}` +
			`]}`,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package:     "com.example.app",
		Track:       "production",
		ReleaseName: "v2.0-rc2",
		Confirm:     true,
	})
	if err != nil {
		t.Fatalf("Complete with --release-name v2.0-rc2: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("result.VersionCode = %d, want 501 (the rc2 release)", result.VersionCode)
	}
}

// TestState_noReleasesOnTrack_returnsExit30 asserts that an existing track
// with zero releases surfaces as "release not found" (exit 30), distinct
// from the ambiguous case (60). Promote treats this as exit 2; the rollout
// family follows issue #46's exit-30 contract instead — hence its own picker.
func TestState_noReleasesOnTrack_returnsExit30(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-empty",
		sourceTrackGetResp: `{"track":"production","releases":[]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update called despite no releases: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
	})
	if err == nil {
		t.Fatal("Halt(no releases on track): want error, got nil")
	}
	var notFound *orchestrator.ReleaseNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v (%T), want *orchestrator.ReleaseNotFoundError", err, err)
	}
	if notFound.ExitCode() != 30 {
		t.Errorf("ExitCode() = %d, want 30", notFound.ExitCode())
	}
}

// TestState_versionCodeNotFound_returnsExit30 asserts that a --version-code
// matching nothing on the track is "release not found" (exit 30).
func TestState_versionCodeNotFound_returnsExit30(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-nomatch",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"600","status":"inProgress","versionCodes":["600"],"userFraction":0.1}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update called despite no matching release: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Resume(context.Background(), hc, orchestrator.StateOpts{
		Package:     "com.example.app",
		Track:       "production",
		VersionCode: 999,
		Confirm:     true,
	})
	if err == nil {
		t.Fatal("Resume(unknown --version-code): want error, got nil")
	}
	var notFound *orchestrator.ReleaseNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v (%T), want *orchestrator.ReleaseNotFoundError", err, err)
	}
	if notFound.ExitCode() != 30 {
		t.Errorf("ExitCode() = %d, want 30", notFound.ExitCode())
	}
}

// TestState_trackGet404_returnsExit30 asserts a 404 from tracks.get (the
// track itself does not exist) surfaces as exit 30 via api.Error.
func TestState_trackGet404_returnsExit30(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "edit-404",
		getHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(404, `{"error":{"code":404,"message":"track not found"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "nonexistent",
	})
	if err == nil {
		t.Fatal("Halt(track 404): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 30 {
		t.Errorf("ExitCode() = %d, want 30", coder.ExitCode())
	}
}

// TestRollout_fractionOutOfRange_returnsExit2 asserts the CLI-misuse guard
// from AC6: --to 0, > 1, or otherwise out of (0, 1] is rejected before any
// HTTP with exit 2 and a range hint.
func TestRollout_fractionOutOfRange_returnsExit2(t *testing.T) {
	cases := []struct {
		name     string
		fraction float64
	}{
		{"zero", 0},
		{"above one", 1.5},
		{"negative", -0.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &stateRT{
				t:      t,
				editID: "should-not-open",
				insertHandler: func(req *http.Request) (*http.Response, error) {
					t.Errorf("insert called despite invalid fraction: %s", req.URL.Path)
					return jsonResp(500, ""), nil
				},
			}
			hc := &http.Client{Transport: rt}

			_, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
				Package:      "com.example.app",
				Track:        "production",
				UserFraction: tc.fraction,
			})
			if err == nil {
				t.Fatal("Rollout(out-of-range fraction): want error, got nil")
			}
			var coder interface{ ExitCode() int }
			if !errors.As(err, &coder) {
				t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
			}
			if coder.ExitCode() != 2 {
				t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
			}
			if len(rt.calls) != 0 {
				t.Errorf("expected zero HTTP calls before validation, saw: %v", rt.calls)
			}
		})
	}
}

// TestState_missingTrack_returnsExit2 asserts the orchestrator rejects an
// empty Track before any HTTP — symmetric with promote's validateOpts so
// library / future-MCP callers do not burn an Edit on bad input.
func TestState_missingTrack_returnsExit2(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("insert called despite missing Track: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
	})
	if err == nil {
		t.Fatal("Complete(missing Track): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

// TestState_trackUpdateFail_triggersEditDelete is AC7: a 5xx on
// tracks.update must auto-discard the orphan Edit, same as upload/promote.
func TestState_trackUpdateFail_triggersEditDelete(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-state-fail",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"700","status":"inProgress","versionCodes":["700"],"userFraction":0.1}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(503, `{"error":{"code":503,"message":"service unavailable"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
		Package:      "com.example.app",
		Track:        "production",
		UserFraction: 0.2,
		Confirm:      true,
	})
	if err == nil {
		t.Fatal("Rollout: want error after track update failure, got nil")
	}
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-state-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after track update failure; calls = %v", rt.calls)
	}
}

// TestState_trackUpdateFail_keepOnFailure_doesNotDelete asserts the
// --keep-edit-on-failure opt-out: the orphan Edit is left open and the
// error is wrapped in *edits.DanglingEditError carrying the Edit ID.
func TestState_trackUpdateFail_keepOnFailure_doesNotDelete(t *testing.T) {
	rt := &stateRT{
		t:                  t,
		editID:             "edit-state-keep",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"701","status":"inProgress","versionCodes":["701"],"userFraction":0.1}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(503, `{"error":{"code":503,"message":"service unavailable"}}`), nil
		},
		deleteHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("DELETE called despite KeepEditOnFailure=true: %s", req.URL.Path)
			return jsonResp(204, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
		Package:           "com.example.app",
		Track:             "production",
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Halt: want error after track update failure, got nil")
	}
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-state-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-state-keep", dangling.EditID)
	}
}

// TestRollout_dryRun_makesNoHTTPCalls asserts --dry-run previews the
// planned transition with zero HTTP calls (no Edit opened).
func TestRollout_dryRun_makesNoHTTPCalls(t *testing.T) {
	rt := &stateRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("HTTP call in dry-run mode: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
		Package:      "com.example.app",
		Track:        "production",
		UserFraction: 0.05,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Rollout(dry-run): %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run made HTTP calls: %v", rt.calls)
	}
	if result.Status != "inProgress" {
		t.Errorf("result.Status = %q, want inProgress", result.Status)
	}
	if result.UserFraction != 0.05 {
		t.Errorf("result.UserFraction = %v, want 0.05", result.UserFraction)
	}
	if result.ReleaseName != "(dry-run)" {
		t.Errorf("result.ReleaseName = %q, want (dry-run)", result.ReleaseName)
	}
	if result.Track != "production" {
		t.Errorf("result.Track = %q, want production", result.Track)
	}
}

// TestComplete_dryRun_previewsCompleted asserts dry-run also previews the
// terminal transitions (complete → completed/1.0) without HTTP.
func TestComplete_dryRun_previewsCompleted(t *testing.T) {
	rt := &stateRT{t: t}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "production",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("Complete(dry-run): %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run made HTTP calls: %v", rt.calls)
	}
	if result.Status != "completed" || result.UserFraction != 1.0 {
		t.Errorf("result = (%q, %v), want (completed, 1.0)", result.Status, result.UserFraction)
	}
}

// TestRollout_dryRun_invalidFractionStillCaught asserts validation runs
// even in dry-run mode, so a bad preview is not silently "OK".
func TestRollout_dryRun_invalidFractionStillCaught(t *testing.T) {
	rt := &stateRT{t: t}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
		Package:      "com.example.app",
		Track:        "production",
		UserFraction: 2.0,
		DryRun:       true,
	})
	if err == nil {
		t.Fatal("Rollout(dry-run, bad fraction): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

// TestState_confirmGate verifies the ADR-0002 production confirm guard for
// the rollout family: rollout / resume / complete on production reach real
// users and need Confirm=true; halt does not (it reduces exposure); and
// non-production tracks never need it.
func TestState_confirmGate(t *testing.T) {
	const onProd = `{"track":"production","releases":[{"name":"800","status":"inProgress","versionCodes":["800"],"userFraction":0.2}]}`
	const onBeta = `{"track":"beta","releases":[{"name":"800","status":"inProgress","versionCodes":["800"],"userFraction":0.2}]}`

	// complete on production without --confirm → refused before any HTTP.
	t.Run("complete production without confirm refuses", func(t *testing.T) {
		rt := &stateRT{
			t:      t,
			editID: "should-not-open",
			insertHandler: func(req *http.Request) (*http.Response, error) {
				t.Errorf("insert called despite missing confirm: %s", req.URL.Path)
				return jsonResp(500, ""), nil
			},
		}
		hc := &http.Client{Transport: rt}
		_, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
			Package: "com.example.app", Track: "production",
		})
		if err == nil {
			t.Fatal("complete on production without confirm: want error, got nil")
		}
		var confirmErr *exit.SafetyFlagError
		if !errors.As(err, &confirmErr) {
			t.Fatalf("err = %v (%T), want *exit.SafetyFlagError", err, err)
		}
		if confirmErr.ExitCode() != 3 {
			t.Errorf("ExitCode() = %d, want 3 (safety flag required, #408)", confirmErr.ExitCode())
		}
		if confirmErr.Flag != "confirm" {
			t.Errorf("Flag = %q, want %q (feeds requires[] in the JSON envelope)", confirmErr.Flag, "confirm")
		}
		if len(rt.calls) != 0 {
			t.Errorf("expected zero HTTP calls before confirm guard, saw: %v", rt.calls)
		}
	})

	// halt on production without --confirm → allowed (reduces exposure).
	t.Run("halt production without confirm proceeds", func(t *testing.T) {
		rt := &stateRT{t: t, editID: "edit-halt-noconfirm", sourceTrackGetResp: onProd, trackUpdateRawResp: `{}`}
		hc := &http.Client{Transport: rt}
		if _, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
			Package: "com.example.app", Track: "production",
		}); err != nil {
			t.Fatalf("Halt on production without confirm should proceed: %v", err)
		}
	})

	// complete on a non-production track without --confirm → allowed.
	t.Run("complete beta without confirm proceeds", func(t *testing.T) {
		rt := &stateRT{t: t, editID: "edit-complete-beta", sourceTrackGetResp: onBeta, trackUpdateRawResp: `{}`}
		hc := &http.Client{Transport: rt}
		if _, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
			Package: "com.example.app", Track: "beta",
		}); err != nil {
			t.Fatalf("Complete on beta without confirm should proceed: %v", err)
		}
	})

	// rollout on production WITH --confirm → allowed.
	t.Run("rollout production with confirm proceeds", func(t *testing.T) {
		rt := &stateRT{t: t, editID: "edit-rollout-confirm", sourceTrackGetResp: onProd, trackUpdateRawResp: `{}`}
		hc := &http.Client{Transport: rt}
		if _, err := orchestrator.Rollout(context.Background(), hc, orchestrator.StateOpts{
			Package: "com.example.app", Track: "production", UserFraction: 0.5, Confirm: true,
		}); err != nil {
			t.Fatalf("Rollout on production with confirm should proceed: %v", err)
		}
	})
}
