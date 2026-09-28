package detailscmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

// TestRenderJSON_view_emptyRawResponse_fallsBackToPayloadShape pins the
// silent fallback of `apps details view`: a Payload that lost its details.get
// body renders the typed record rather than nothing, through
// output.WriteJSON (no HTML escaping of the website query string).
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

// TestRenderJSON_set_dryRun_golden freezes the gplay-authored `apps details
// set --dry-run` preview: only the fields the patch would touch, tagged
// dryRun so it cannot pass for a real details.patch echo.
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
