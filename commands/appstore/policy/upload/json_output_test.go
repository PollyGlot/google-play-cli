package upload_test

import (
	"testing"

	policy "github.com/PollyGlot/google-play-cli/commands/appstore/policy/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := policy.Payload{StorePackage: "com.example.store", Package: "com.example.app", Path: "docs/privacy & data policy.pdf", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
