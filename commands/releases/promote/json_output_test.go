package promote_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// promoteUpdateBody is a tracks.update response as Google shapes it, with the
// characters an encoder would rewrite (&, <, >) and a layout gplay would not
// produce itself: passthrough means these exact bytes reach stdout.
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

// TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim drives Run against
// the fake API and asserts `--output json` prints the tracks.update body
// byte for byte (ADR-0003): the contract agents and storedeck parse.
func TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-xyz",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"]}]}`,
		trackUpdateRawResp: promoteUpdateBody,
	}
	rc, _ := newRC(t, rt)

	r, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "internal", ToTrack: "beta"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(promoteUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, promoteUpdateBody)
	}
}

// TestRenderJSON_emptyRawResponse_fallsBackToResultShape pins the silent
// fallback (also the --dry-run output, which has no API body): a Result
// without a raw tracks.update body renders the gplay Result shape rather
// than nothing, through output.WriteJSON (no HTML escaping of the name).
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
