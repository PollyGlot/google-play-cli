package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/recovery/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run.json.golden", create.Payload{Package: "com.example.app", DryRun: true})
}
