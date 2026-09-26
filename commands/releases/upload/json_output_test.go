package upload_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// trackUpdateBody is a tracks.update response as Google shapes it, with the
// characters an encoder would rewrite (&, <, >) and a layout gplay would not
// produce itself: passthrough means these exact bytes reach stdout.
const trackUpdateBody = `{
 "track": "internal",
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
	rt := &uploadRT{t: t, editID: "edit-xyz", versionCode: 142, trackUpdateRawResp: trackUpdateBody}
	rc, _ := newRC(t, rt)

	r, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(trackUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, trackUpdateBody)
	}
}

// TestRenderJSON_dryRun_golden freezes the gplay-authored shape --dry-run
// prints: no request ran, so there is no API body to pass through.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	rc, _ := newRC(t, &uploadRT{t: t})

	r, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: writeFakeAAB(t), DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", r)
}

// TestRenderJSON_emptyRawResponse_fallsBackToResultShape pins the silent
// fallback: a Result that lost its raw tracks.update body renders the gplay
// Result shape rather than nothing, through output.WriteJSON (no HTML
// escaping of the release name).
func TestRenderJSON_emptyRawResponse_fallsBackToResultShape(t *testing.T) {
	p := upload.Payload{Result: &orchestrator.Result{
		VersionCode:     142,
		Track:           "beta",
		ReleaseName:     "142 <rc> & hotfix",
		Status:          "inProgress",
		UserFraction:    0.25,
		DefaultLanguage: "en-US",
		Locales:         []string{"en-US", "fr-FR"},
	}}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
