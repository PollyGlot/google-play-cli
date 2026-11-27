package orchestrator_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestReleaseSelection_exitCodesAndMessages(t *testing.T) {
	const (
		empty = `{"track":"beta","releases":[]}`
		one   = `{"track":"beta","releases":[{"name":"v1","status":"completed","versionCodes":["100"]}]}`
		two   = `{"track":"beta","releases":[` +
			`{"name":"v1","status":"halted","versionCodes":["100"],"userFraction":0.1},` +
			`{"name":"v1","status":"inProgress","versionCodes":["101"],"userFraction":0.2}]}`
	)
	promote := func(vc int, name string, plan bool) func(*http.Client) error {
		return func(hc *http.Client) error {
			_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "beta", ToTrack: "internal",
				VersionCode: vc, ReleaseName: name, Plan: plan,
			})
			return err
		}
	}
	halt := func(vc int, name string, plan bool) func(*http.Client) error {
		return func(hc *http.Client) error {
			_, err := orchestrator.Halt(context.Background(), hc, orchestrator.StateOpts{
				Package: "com.example.app", Track: "beta",
				VersionCode: vc, ReleaseName: name, Plan: plan,
			})
			return err
		}
	}
	complete := func(vc int) func(*http.Client) error {
		return func(hc *http.Client) error {
			_, err := orchestrator.Complete(context.Background(), hc, orchestrator.StateOpts{
				Package: "com.example.app", Track: "beta", VersionCode: vc,
			})
			return err
		}
	}

	cases := []struct {
		name     string
		track    string
		run      func(*http.Client) error
		wantExit int
		wantMsg  string
	}{
		{"promote empty", empty, promote(0, "", false), 2,
			"source track beta has no releases to promote"},
		{"promote empty filtered", empty, promote(100, "", false), 2,
			"source track beta has no releases to promote"},
		{"promote plan empty", empty, promote(0, "", true), 2,
			"source track beta has no releases to promote"},
		{"promote no match", one, promote(999, "", false), 60,
			"no release on source track beta matches --version-code=999. Available: v1 [versionCodes=100]"},
		{"promote no match both filters", one, promote(100, "v2", false), 60,
			"no release on source track beta matches --version-code=100 AND --release-name=v2. Available: v1 [versionCodes=100]"},
		{"promote no match name matches", one, promote(999, "v1", false), 60,
			"no release on source track beta matches --version-code=999 AND --release-name=v1. Available: v1 [versionCodes=100]"},
		{"promote plan no match", one, promote(0, "v2", true), 60,
			"no release on source track beta matches --release-name=v2. Available: v1 [versionCodes=100]"},
		{"promote ambiguous", two, promote(0, "", false), 60,
			"track beta has multiple coexisting releases: pass --version-code or --release-name to pick one. Candidates: v1 [halted, versionCodes=100], v1 [inProgress, versionCodes=101]"},
		{"promote ambiguous filtered", two, promote(0, "v1", false), 60,
			"track beta has multiple coexisting releases: the supplied --version-code/--release-name still matched more than one; tighten by combining both filters or pick a more specific value. Candidates: v1 [halted, versionCodes=100], v1 [inProgress, versionCodes=101]"},
		{"halt empty", empty, halt(0, "", false), 30,
			"track beta has no releases to operate on"},
		{"halt empty filtered", empty, halt(100, "v1", false), 30,
			"no release on track beta matches --version-code=100 AND --release-name=v1"},
		{"halt plan empty", empty, halt(0, "", true), 30,
			"track beta has no releases to operate on"},
		{"halt no match", one, halt(0, "v2", false), 30,
			"no release on track beta matches --release-name=v2. Available: v1 [versionCodes=100]"},
		{"halt no match name matches", one, halt(999, "v1", false), 30,
			"no release on track beta matches --version-code=999 AND --release-name=v1. Available: v1 [versionCodes=100]"},
		{"complete no match", one, complete(999), 30,
			"no release on track beta matches --version-code=999. Available: v1 [versionCodes=100]"},
		{"halt plan no match", one, halt(999, "", true), 30,
			"no release on track beta matches --version-code=999. Available: v1 [versionCodes=100]"},
		{"halt ambiguous", two, halt(0, "", false), 60,
			"track beta has multiple coexisting releases: pass --version-code or --release-name to pick one. Candidates: v1 [halted, versionCodes=100], v1 [inProgress, versionCodes=101]"},
		{"halt ambiguous filtered", two, halt(0, "v1", true), 60,
			"track beta has multiple coexisting releases: the supplied --version-code/--release-name still matched more than one; tighten by combining both filters or pick a more specific value. Candidates: v1 [halted, versionCodes=100], v1 [inProgress, versionCodes=101]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newPromoteFake(promoteAPI{
				editID:             "edit-pick",
				sourceTrackGetResp: tc.track,
				updateHandler: func(c testkit.Call) (int, string) {
					t.Errorf("tracks.update called despite no single release: %s", c.Path)
					return 500, ""
				},
			})
			err := tc.run(&http.Client{Transport: rt})
			if got := exit.For(err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d (err = %v)", got, tc.wantExit, err)
			}
			if err == nil || err.Error() != tc.wantMsg {
				t.Errorf("err = %v\nwant %s", err, tc.wantMsg)
			}
		})
	}
}
