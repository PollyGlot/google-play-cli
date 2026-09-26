package upload_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the gplay-authored --dry-run report:
// no upload ran, so there is no InternalAppSharingArtifact to pass through
// (the live branch writes p.Raw verbatim and is out of golden scope).
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := upload.Payload{Package: "com.example.app", Kind: "bundle", Bytes: 5242880, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
