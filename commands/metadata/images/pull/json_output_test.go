package imagespull

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// TestRenderJSON_pulled_golden freezes the summary of what pull wrote, built
// by the real newPayload: slots in (locale, canonical type) order whatever the
// map order, empty slots skipped, and the three totals a CI gate reads.
func TestRenderJSON_pulled_golden(t *testing.T) {
	png := []byte("png")
	tr := imagetree.Tree{
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

// TestRenderJSON_nothingPulled_golden pins an app with no image to
// "pulled": [] (never null) and zero totals.
func TestRenderJSON_nothingPulled_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "nothing_pulled.json.golden", newPayload("com.example.app", "./metadata", imagetree.Tree{}))
}
