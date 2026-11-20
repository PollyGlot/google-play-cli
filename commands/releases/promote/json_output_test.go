package promote_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

const promoteUpdateBody = `{
 "track": "beta",
 "releases": [
  {
   "name": "142",
   "versionCodes": ["142"],
   "status": "completed",
   "releaseNotes": [{"language": "en-US", "text": "Fixes <crash> on start & faster sync"}]
  }
 ]
}`

func TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-xyz",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"]}]}`,
		trackUpdateRawResp: promoteUpdateBody,
	})
	rc, _ := newRC(t, rt)

	r, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "internal", ToTrack: "beta"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(promoteUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, promoteUpdateBody)
	}
}

func TestRenderJSON_emptyRawResponse_fallsBackToResultShape(t *testing.T) {
	p := promote.Payload{Result: &orchestrator.Result{
		VersionCode:     142,
		Track:           "production",
		ReleaseName:     "142 <rc> & hotfix",
		Status:          "inProgress",
		UserFraction:    0.05,
		DefaultLanguage: "en-US",
		Locales:         []string{"en-US", "fr-FR"},
	}}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
