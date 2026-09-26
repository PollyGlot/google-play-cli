package signingcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview shared by enroll
// and rotate. The live path passes the API body through (ADR-0003) and has no
// golden.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := signingcmd.Payload{Verb: "rotate", Package: "com.example.app", Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}
