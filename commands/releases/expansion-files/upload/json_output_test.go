package upload_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := upload.Payload{VersionCode: 142, Type: "patch", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
