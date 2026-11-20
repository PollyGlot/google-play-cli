package imagespull

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func TestRenderJSON_pulled_golden(t *testing.T) {
	const png = "1"
	tr := imagetree.Staged{
		"fr-FR": {images.PhoneScreenshots: {png}},
		"en-US": {
			images.PhoneScreenshots: {png, png, png},
			images.Icon:             {png},
			images.TvBanner:         nil,
			images.FeatureGraphic:   {png},
		},
	}
	outputtest.GoldenJSON(t, "pulled.json.golden", newPayload("com.example.app", "./metadata", tr))
}

func TestRenderJSON_nothingPulled_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "nothing_pulled.json.golden", newPayload("com.example.app", "./metadata", imagetree.Staged{}))
}
