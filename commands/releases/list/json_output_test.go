package list_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/list"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

func TestRenderJSON_emptyRawResponse_fallsBackToPayloadShape(t *testing.T) {
	p := list.Payload{
		Track: "production",
		Releases: []tracks.Release{
			{
				Name:         "142 <rc> & hotfix",
				Status:       "inProgress",
				UserFraction: 0.1,
				VersionCodes: []string{"142"},
				ReleaseNotes: []tracks.LocalizedText{{Language: "en-US", Text: "Fixes <crash> on start & faster sync"}},
			},
			{Name: "141", Status: "completed", VersionCodes: []string{"141"}},
		},
	}
	outputtest.GoldenJSON(t, "fallback.json.golden", p)
}
