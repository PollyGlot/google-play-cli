package policy_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/upload/policy"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the gplay-shaped --dry-run preview,
// fileType included since the request always carries it; the live path passes
// the API body through (ADR-0003). The & in the path must stay unescaped.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := policy.Payload{StorePackage: "com.example.store", Package: "com.example.app", Path: "docs/privacy & data policy.pdf", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
