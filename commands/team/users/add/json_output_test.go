package add_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/users/add"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview of the member to
// invite. Nil requires prints [] (a routine write needs no safety flag) and
// warnings is omitted; the live path passes the API body through (ADR-0003).
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := add.Payload{
		Email:       "dev@example.com",
		Permissions: []string{"CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", "CAN_REPLY_TO_REVIEWS_GLOBAL"},
		DryRun:      true,
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_dryRunAdminWithWarning_golden freezes the admin gate and a
// steering warning, the two optional parts of the preview.
func TestRenderJSON_dryRunAdminWithWarning_golden(t *testing.T) {
	p := add.Payload{
		Email:       "admin@example.com",
		Permissions: []string{"CAN_MANAGE_PERMISSIONS_GLOBAL", "CAN_SEE_ALL_APPS"},
		Requires:    []string{"grant-admin"},
		Warnings:    []string{"permission CAN_SEE_ALL_APPS is deprecated; prefer CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL"},
		DryRun:      true,
	}
	outputtest.GoldenJSON(t, "dry_run_admin_warning.json.golden", p)
}
