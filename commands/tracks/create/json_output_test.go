package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/tracks/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// TestRenderJSON_dryRun_golden freezes the gplay-authored TrackConfig preview
// --dry-run prints. Every Payload field is json:"-", so this hand-built shape
// is the only JSON a preview can emit (the live branch writes p.Raw verbatim).
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := create.Payload{
		Name:       "qa-team",
		Type:       tracks.TrackTypeClosedTesting,
		FormFactor: tracks.FormFactorDefault,
		Kind:       "custom",
		DryRun:     true,
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
