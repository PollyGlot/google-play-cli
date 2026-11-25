package upload_test

import (
	"bytes"
	"encoding/json"
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

func TestRenderJSON_dryRun_requiresTheFlagsTheLiveRunNeeds(t *testing.T) {
	cases := []struct {
		name  string
		input upload.Input
		want  string
	}{
		{"production staged", upload.Input{Track: "production", StagedFraction: 0.1, StagedFractionSet: true}, `["confirm"]`},
		{"production complete", upload.Input{Track: "production", Complete: true}, `["confirm"]`},
		{"beta", upload.Input{Track: "beta"}, `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, transport := newUploadTransport(uploadAPI{})
			rc, _ := newRC(t, transport)
			in := tc.input
			in.Package, in.Artifacts, in.DryRun = "com.example.app", []string{writeFakeAAB(t)}, true
			r, err := upload.Run(rc, in)
			if err != nil {
				t.Fatalf("Run --dry-run: %v", err)
			}
			if touched(rt) {
				t.Errorf("dry-run calls = %v, want none", apiCalls(rt))
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(outputtest.RenderJSON(t, r), &got); err != nil {
				t.Fatalf("decode dry-run JSON: %v", err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, got["requires"]); err != nil || compact.String() != tc.want {
				t.Errorf("requires = %s, want %s", got["requires"], tc.want)
			}
		})
	}
}

func TestRenderJSON_liveFallback_carriesNoRequires(t *testing.T) {
	p := upload.Payload{Result: &orchestrator.Result{Track: "production", Status: "inProgress", Requires: []string{"confirm"}}}
	if got := outputtest.RenderJSON(t, p); bytes.Contains(got, []byte(`"requires"`)) {
		t.Errorf("live JSON = %s, want no requires key", got)
	}
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
