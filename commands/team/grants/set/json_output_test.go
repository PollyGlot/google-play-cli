package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/grants/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRunUpdate_golden freezes the --dry-run diff for an existing
// grant: add/remove are derived and sorted, and the steering warning is kept.
// The live path passes the API body through (ADR-0003) and has no golden.
func TestRenderJSON_dryRunUpdate_golden(t *testing.T) {
	p := set.Payload{
		Email:    "dev@example.com",
		Package:  "com.example.app",
		Verb:     "update",
		Current:  []string{"CAN_ACCESS_APP", "CAN_REPLY_TO_REVIEWS"},
		Desired:  []string{"CAN_REPLY_TO_REVIEWS", "CAN_MANAGE_TRACK_APKS", "CAN_ACCESS_APP"},
		Warnings: []string{"permission CAN_ACCESS_APP is deprecated; prefer CAN_VIEW_NON_FINANCIAL_DATA"},
		DryRun:   true,
	}
	outputtest.GoldenJSON(t, "dry_run_update.json.golden", p)
}

// TestRenderJSON_dryRunCreateAdmin_golden freezes a new admin-conferring grant:
// empty current and remove print [], requires names grant-admin, and warnings
// is omitted when there are none.
func TestRenderJSON_dryRunCreateAdmin_golden(t *testing.T) {
	p := set.Payload{
		Email:    "admin@example.com",
		Package:  "com.example.app",
		Verb:     "create",
		Desired:  []string{"CAN_MANAGE_PERMISSIONS"},
		Requires: []string{"grant-admin"},
		DryRun:   true,
	}
	outputtest.GoldenJSON(t, "dry_run_create_admin.json.golden", p)
}
