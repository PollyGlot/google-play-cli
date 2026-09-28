package imagesvalidate_test

import (
	"testing"

	imagesvalidate "github.com/PollyGlot/google-play-cli/commands/metadata/images/validate"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_ok_golden freezes the offline success object. It reads the
// real RulesVersion, as Run does, so a rule-table bump shows up here as a
// deliberate golden diff. The dir echoes --dir verbatim, hence the &.
func TestRenderJSON_ok_golden(t *testing.T) {
	p := imagesvalidate.Payload{Dir: "./R&D/metadata", Slots: 4, Images: 9, RulesVersion: imagevalidate.RulesVersion, OK: true}
	outputtest.GoldenJSON(t, "ok.json.golden", p)
}
