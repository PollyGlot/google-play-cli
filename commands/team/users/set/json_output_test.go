package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/users/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRun_golden freezes the --dry-run preview of the replacement
// set, admin gate and steering warning included. The live path passes the API
// body through (ADR-0003) and has no golden.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := set.Payload{
		Email:       "admin@example.com",
		Permissions: []string{"CAN_MANAGE_PERMISSIONS_GLOBAL", "CAN_SEE_ALL_APPS"},
		Requires:    []string{"grant-admin"},
		Warnings:    []string{"permission CAN_SEE_ALL_APPS is deprecated; prefer CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL"},
		DryRun:      true,
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

// TestRenderJSON_dryRunClear_golden freezes --clear: nil permissions and
// requires print [] (never null) and warnings is omitted.
func TestRenderJSON_dryRunClear_golden(t *testing.T) {
	p := set.Payload{Email: "dev@example.com", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run_clear.json.golden", p)
}
