package upload_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := upload.Payload{Package: "com.example.app", Kind: "bundle", Bytes: 5242880, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
