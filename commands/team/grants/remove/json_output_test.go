package remove_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/grants/remove"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := remove.Payload{Email: "dev@example.com", Package: "com.example.app", Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := remove.Payload{Email: "dev@example.com", Package: "com.example.app"}
	outputtest.GoldenJSON(t, "removed_empty_body.json.golden", p)
}
