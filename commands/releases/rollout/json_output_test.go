package rollout_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// rolloutUpdateBody is a tracks.update response as Google shapes it, with the
// characters an encoder would rewrite (&, <, >) and a layout gplay would not
// produce itself: passthrough means these exact bytes reach stdout.
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

// TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim drives RunRollout
// against the fake API and asserts `--output json` prints the tracks.update
// body byte for byte (ADR-0003). halt/resume/complete share the renderer.
func TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-xyz",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: rolloutUpdateBody,
	})
	rc := newRC(t, rt)

	r, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", To: "0.2", ToSet: true, Confirm: true})
	if err != nil {
		t.Fatalf("RunRollout: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(rolloutUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, rolloutUpdateBody)
	}
}

// TestRenderJSON_emptyRawResponse_fallsBackToResultShape pins the silent
// fallback (also the --dry-run output, which has no API body): a Result
// without a raw tracks.update body renders the gplay Result shape rather
// than nothing, through output.WriteJSON (no HTML escaping of the name).
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
