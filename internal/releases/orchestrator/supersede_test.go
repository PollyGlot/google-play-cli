package orchestrator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// releasesSent decodes the releases array of the tracks.update body, each
// release kept as the exact bytes gplay sent.
func releasesSent(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var track struct {
		Releases []json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(body, &track); err != nil {
		t.Fatalf("decode tracks.update body %s: %v", body, err)
	}
	return track.Releases
}

// TestComplete_supersedesThePreviousCompletedRelease is #734: during a
// staged rollout the track holds the previous full rollout as a completed
// release, and Play refuses an update that carries two ("Only one completed
// release is allowed"). Completing the rollout drops the previous completed
// release, keeps the halted and draft ones byte for byte, keeps the
// target's unmodelled fields, and reports the superseded versionCode.
func TestComplete_supersedesThePreviousCompletedRelease(t *testing.T) {
	const (
		previous = `{"name":"400","status":"completed","versionCodes":["400"]}`
		halted   = `{"name":"399","status":"halted","versionCodes":["399"],"userFraction":0.1,"countryTargeting":{"countries":["FR"]}}`
		target   = `{"name":"401","status":"inProgress","versionCodes":["401"],"userFraction":0.2,"countryTargeting":{"countries":["DE"]},"inAppUpdatePriority":3}`
		draft    = `{"name":"402","status":"draft","versionCodes":["402"]}`
	)
	rt := newPromoteFake(stateAPI{
		editID:             "edit-supersede",
		sourceTrackGetResp: `{"track":"production","releases":[` + previous + `,` + halted + `,` + target + `,` + draft + `]}`,
		trackUpdateRawResp: `{"track":"production","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package:     "com.example.app",
		Track:       "production",
		VersionCode: 401,
		Confirm:     true,
	})
	if err != nil {
		t.Fatalf("Complete --version-code 401: %v", err)
	}

	sent := releasesSent(t, trackUpdateReq(rt))
	if len(sent) != 3 {
		t.Fatalf("tracks.update carries %d releases, want 3 (halted, completed target, draft): %s", len(sent), trackUpdateReq(rt))
	}
	if got := string(sent[0]); got != halted {
		t.Errorf("halted release sent as %s, want it byte for byte: %s", got, halted)
	}
	if got := string(sent[2]); got != draft {
		t.Errorf("draft release sent as %s, want it byte for byte: %s", got, draft)
	}

	var completed map[string]json.RawMessage
	if err := json.Unmarshal(sent[1], &completed); err != nil {
		t.Fatalf("decode target release %s: %v", sent[1], err)
	}
	if got := string(completed["status"]); got != `"completed"` {
		t.Errorf("target status = %s, want \"completed\"", got)
	}
	if raw, has := completed["userFraction"]; has {
		t.Errorf("target kept userFraction %s, want the key removed", raw)
	}
	if got := string(completed["countryTargeting"]); got != `{"countries":["DE"]}` {
		t.Errorf("target countryTargeting = %s, want it kept", got)
	}
	if got := string(completed["inAppUpdatePriority"]); got != "3" {
		t.Errorf("target inAppUpdatePriority = %s, want it kept", got)
	}

	if !slices.Equal(result.SupersededVersionCodes, []int{400}) {
		t.Errorf("SupersededVersionCodes = %v, want [400]", result.SupersededVersionCodes)
	}
}

// TestState_onlyCompleteSupersedes asserts the other transitions leave a
// completed release in place: Play admits one completed release next to an
// inProgress or halted one, so rollout, halt and resume must not drop it.
func TestState_onlyCompleteSupersedes(t *testing.T) {
	const (
		completed = `{"name":"400","status":"completed","versionCodes":["400"]}`
		staged    = `{"name":"401","status":"inProgress","versionCodes":["401"],"userFraction":0.2}`
	)
	verbs := []struct {
		name string
		run  func(context.Context, *http.Client, orchestrator.StateOpts) (*orchestrator.Result, error)
	}{
		{"rollout", orchestrator.Rollout},
		{"halt", orchestrator.Halt},
		{"resume", orchestrator.Resume},
	}
	for _, v := range verbs {
		t.Run(v.name, func(t *testing.T) {
			rt := newPromoteFake(stateAPI{
				editID:             "edit-" + v.name,
				sourceTrackGetResp: `{"track":"production","releases":[` + completed + `,` + staged + `]}`,
				trackUpdateRawResp: `{"track":"production","releases":[]}`,
			})
			hc := &http.Client{Transport: rt}

			result, err := v.run(context.Background(), hc, orchestrator.StateOpts{
				Package:      "com.example.app",
				Track:        "production",
				VersionCode:  401,
				UserFraction: 0.5,
				Confirm:      true,
			})
			if err != nil {
				t.Fatalf("%s: %v", v.name, err)
			}
			sent := releasesSent(t, trackUpdateReq(rt))
			if len(sent) != 2 || string(sent[0]) != completed {
				t.Errorf("tracks.update releases = %s, want the completed release kept byte for byte first", trackUpdateReq(rt))
			}
			if len(result.SupersededVersionCodes) != 0 {
				t.Errorf("SupersededVersionCodes = %v, want none", result.SupersededVersionCodes)
			}
		})
	}
}

// TestComplete_aloneOnTrack_supersedesNothing keeps the single-release case
// unchanged: completing the only release drops nothing.
func TestComplete_aloneOnTrack_supersedesNothing(t *testing.T) {
	rt := newPromoteFake(stateAPI{
		editID:             "edit-alone",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"401","status":"inProgress","versionCodes":["401"],"userFraction":0.2}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	})
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
		Package: "com.example.app",
		Track:   "beta",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if sent := releasesSent(t, trackUpdateReq(rt)); len(sent) != 1 {
		t.Errorf("tracks.update carries %d releases, want 1", len(sent))
	}
	if len(result.SupersededVersionCodes) != 0 {
		t.Errorf("SupersededVersionCodes = %v, want none", result.SupersededVersionCodes)
	}
}
