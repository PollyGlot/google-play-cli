package remove_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/users/remove"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview an agent reads to
// learn the destructive gate (ADR-0017 §4) before deleting the member.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := remove.Payload{Email: "dev@example.com", Requires: []string{"confirm"}, DryRun: true}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_emptyBody_golden freezes the success object that stands in
// for users.delete's empty body; a non-empty body passes through instead.
func TestRenderJSON_emptyBody_golden(t *testing.T) {
	p := remove.Payload{Email: "dev@example.com"}
	outputtest.GoldenJSON(t, "removed_empty_body.json.golden", p)
}
