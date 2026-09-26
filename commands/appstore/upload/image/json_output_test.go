package image_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/upload/image"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the gplay-shaped --dry-run preview; the
// live path passes the API body through (ADR-0003) and has no golden. The path
// carries a & so the golden proves output.WriteJSON leaves it unescaped.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := image.Payload{StorePackage: "com.example.store", Package: "com.example.app", Path: "art/r&d/icon-512.png", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
