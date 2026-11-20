package rollout_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

const rolloutUpdateBody = `{
 "track": "production",
 "releases": [
  {
   "name": "142",
   "versionCodes": ["142"],
   "status": "inProgress",
   "userFraction": 0.2,
   "releaseNotes": [{"language": "en-US", "text": "Fixes <crash> on start & faster sync"}]
  }
 ]
}`

func TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-xyz",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: rolloutUpdateBody,
	})
	rc := newRC(t, rt)

	r, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", StagedFraction: 0.2, StagedFractionSet: true, Confirm: true})
	if err != nil {
		t.Fatalf("RunRollout: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(rolloutUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, rolloutUpdateBody)
	}
}

func TestRenderJSON_emptyRawResponse_fallsBackToResultShape(t *testing.T) {
	p := rollout.Payload{Result: &orchestrator.Result{
		VersionCode:  142,
		Track:        "production",
		ReleaseName:  "142 <rc> & hotfix",
		Status:       "halted",
		UserFraction: 0.2,
	}}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
