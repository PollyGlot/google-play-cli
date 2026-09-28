package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the gplay-authored --dry-run preview:
// no expansionfiles.update ran, so there is no API body (the live branch
// writes p.Raw verbatim and is out of golden scope).
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := set.Payload{VersionCode: 142, Type: "main", ReferencesVersion: 141, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
