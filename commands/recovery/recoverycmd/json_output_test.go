package recoverycmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_lifecycleDryRun_golden freezes the --dry-run preview shared
// by deploy, cancel and add-targeting. The live path passes the API body
// through (ADR-0003) and has no golden.
func TestRenderJSON_lifecycleDryRun_golden(t *testing.T) {
	p := recoverycmd.LifecyclePayload{Verb: "deploy", RecoveryID: "4711", Package: "com.example.app", DryRun: true}
	outputtest.GoldenJSON(t, "lifecycle_dry_run.json.golden", p)
}
