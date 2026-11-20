package set_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/team/users/set"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

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

func TestRenderJSON_dryRunClear_golden(t *testing.T) {
	p := set.Payload{Email: "dev@example.com", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run_clear.json.golden", p)
}
