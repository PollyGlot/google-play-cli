package auditcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/internal/audit"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_clean_golden(t *testing.T) {
	p := auditcmd.Payload{Report: auditcmd.Report{
		Ran:      auditcmd.Ran{Apps: []string{"com.example.app"}, Checks: []string{"lingering-drafts", "no-production-release"}},
		Findings: []audit.Finding{},
		Summary:  auditcmd.Summary{AppsAudited: 1},
	}}
	outputtest.GoldenJSON(t, "clean.json.golden", p)
}

func TestRenderJSON_findingsAndErrors_golden(t *testing.T) {
	p := auditcmd.Payload{Report: auditcmd.Report{
		Ran: auditcmd.Ran{
			Apps:   []string{"com.example.app", "com.example.app.beta"},
			Checks: []string{"lingering-drafts", "empty-release-notes"},
		},
		Findings: []audit.Finding{
			{
				Package:  "com.example.app",
				Check:    "lingering-drafts",
				Severity: audit.SeverityWarning,
				Message:  "track beta holds a draft release <142> & it was never shipped",
				Evidence: map[string]string{"track": "beta", "release": "142"},
			},
			{
				Package:  "com.example.app.beta",
				Check:    "empty-release-notes",
				Severity: audit.SeverityInfo,
				Message:  "release 7 on internal has no release notes",
			},
		},
		Errors: []auditcmd.SweepError{
			{Package: "com.example.locked", Message: "service account is not granted access to \"com.example.locked\"", ExitCode: 11},
		},
		Summary: auditcmd.Summary{AppsAudited: 2, AppsFailed: 1, Findings: 2},
	}}
	outputtest.GoldenJSON(t, "findings_and_errors.json.golden", p)
}
