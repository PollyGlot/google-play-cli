package detailscmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

func TestRenderJSON_view_emptyRawResponse_fallsBackToPayloadShape(t *testing.T) {
	p := detailscmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		ContactEmail:    "dev@example.com",
		ContactPhone:    "+1 555 0100",
		ContactWebsite:  "https://example.com/support?lang=en&ref=<play>",
	}
	outputtest.GoldenJSON(t, "view_fallback.json.golden", p)
}

func TestRenderJSON_set_dryRun_golden(t *testing.T) {
	email := "dev@example.com"
	website := "https://example.com/support?lang=en&ref=<play>"
	p := detailscmd.SetPayload{
		Package: "com.example.app",
		Patch:   details.AppDetailsPatch{ContactEmail: &email, ContactWebsite: &website},
		DryRun:  true,
	}
	outputtest.GoldenJSON(t, "set_dry_run.json.golden", p)
}
