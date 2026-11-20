package recoverycmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_lifecycleDryRun_golden(t *testing.T) {
	p := recoverycmd.LifecyclePayload{Verb: "deploy", RecoveryID: "4711", Package: "com.example.app", DryRun: true}
	outputtest.GoldenJSON(t, "lifecycle_dry_run.json.golden", p)
}
