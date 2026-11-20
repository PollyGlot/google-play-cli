package upload_test

import (
	"testing"

	apk "github.com/PollyGlot/google-play-cli/commands/appstore/apk/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := apk.Payload{StorePackage: "com.example.store", Package: "com.example.app", Path: "build/r&d/app-release.apk", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
