package upload_test

import (
	"bytes"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

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

func TestRenderJSON_passthrough_isTheTracksUpdateBodyVerbatim(t *testing.T) {
	_, transport := newUploadTransport(uploadAPI{editID: "edit-xyz", versionCode: 142, trackUpdateRawResp: trackUpdateBody})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := outputtest.RenderJSON(t, r); !bytes.Equal(got, []byte(trackUpdateBody)) {
		t.Errorf("JSON output is not the tracks.update body verbatim\n got: %s\nwant: %s", got, trackUpdateBody)
	}
}

func TestRenderJSON_dryRun_golden(t *testing.T) {
	_, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)}, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", r)
}

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
