package create_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/customapps/create"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/customapps"
)

func TestRenderJSON_dryRun_golden(t *testing.T) {
	p := create.Payload{
		Account: "1234567890123456789",
		Title:   "Field <Ops> & Logistics",
		Lang:    "en-US",
		Orgs:    []customapps.Organization{{OrganizationID: "org-001", OrganizationName: "Example Corp"}},
		DryRun:  true,
	}
	outputtest.GoldenJSON(t, "dry_run.json.golden", p)
}

func TestRenderJSON_dryRunNoOrgs_golden(t *testing.T) {
	p := create.Payload{Account: "1234567890123456789", Title: "Field Ops", Lang: "fr-FR", DryRun: true}
	outputtest.GoldenJSON(t, "dry_run_no_orgs.json.golden", p)
}
