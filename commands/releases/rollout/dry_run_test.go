package rollout_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func TestRun_dryRun_jsonCarriesDryRunRequiresAndTheRequestedVersionCode(t *testing.T) {
	cases := []struct {
		name         string
		run          func(*kernel.RunContext, rollout.Input) (output.Renderable, error)
		track        string
		wantRequires []string
	}{
		{"rollout on production", rollout.RunRollout, "production", []string{"confirm"}},
		{"resume on production", rollout.RunResume, "production", []string{"confirm"}},
		{"complete on production", rollout.RunComplete, "production", []string{"confirm"}},
		{"halt on production", rollout.RunHalt, "production", []string{}},
		{"complete on beta", rollout.RunComplete, "beta", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newStateFake(stateAPI{editID: "edit-dry", trackGetResp: oneInProgressRelease})
			payload, err := tc.run(newRC(t, rt), rollout.Input{
				Package: "com.example.app", Track: tc.track, VersionCode: 1042,
				StagedFraction: 0.2, StagedFractionSet: true, DryRun: true,
			})
			if err != nil {
				t.Fatalf("dry-run: %v", err)
			}
			if touched(rt) {
				t.Errorf("dry-run calls = %v, want none", apiCalls(rt))
			}
			var js bytes.Buffer
			if err := output.Render(&js, output.FormatJSON, payload.Renderers()); err != nil {
				t.Fatalf("json render: %v", err)
			}
			var got struct {
				DryRun      bool      `json:"dryRun"`
				VersionCode int       `json:"versionCode"`
				Track       string    `json:"track"`
				ReleaseName string    `json:"releaseName"`
				Requires    *[]string `json:"requires"`
			}
			if err := json.Unmarshal(js.Bytes(), &got); err != nil {
				t.Fatalf("dry-run JSON %s: %v", js.String(), err)
			}
			if !got.DryRun || got.VersionCode != 1042 || got.Track != tc.track || got.ReleaseName != "(dry-run)" {
				t.Errorf("dry-run JSON = %s, want dryRun, versionCode 1042, track %s, releaseName (dry-run)", js.String(), tc.track)
			}
			if got.Requires == nil || !slices.Equal(*got.Requires, tc.wantRequires) {
				t.Errorf("dry-run JSON = %s, want requires %v (an array, never absent)", js.String(), tc.wantRequires)
			}
		})
	}
}
