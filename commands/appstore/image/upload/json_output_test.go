package upload_test

import (
	"testing"

	image "github.com/PollyGlot/google-play-cli/commands/appstore/image/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := image.Payload{StorePackage: "com.example.store", Package: "com.example.app", Path: "art/r&d/icon-512.png", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
