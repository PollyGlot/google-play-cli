package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/device-tiers/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run.json.golden", create.Payload{Package: "com.example.app", Bytes: 412, DryRun: true})
}
