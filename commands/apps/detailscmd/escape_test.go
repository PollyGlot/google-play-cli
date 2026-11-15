package detailscmd_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRun_humanViews_stripTerminalEscapes serves details whose contact fields
// carry an OSC title and a CSI erase-screen, and asserts the human views print
// them without the sequences while JSON keeps the bytes verbatim (ADR-0003).
func TestRun_humanViews_stripTerminalEscapes(t *testing.T) {
	body := `{"contactEmail":"hi@example.com` + outputtest.HostileJSON + `","contactWebsite":"https://x.example` + outputtest.HostileJSON + `","defaultLanguage":"en-US"}`
	api := &readAPI{editID: "edit-details", details: body, detailsCode: 200}
	rc, _ := newRC(t, api.serve(t))
	r, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for f, got := range outputtest.RenderHuman(t, r) {
		if !strings.Contains(got, "hi@example.com") || !strings.Contains(got, "https://x.example") {
			t.Errorf("--output %s lost the visible text:\n%s", f, got)
		}
	}
	if js := string(outputtest.RenderJSON(t, r)); !strings.Contains(js, outputtest.HostileJSON) {
		t.Errorf("json must keep the API bytes verbatim, got:\n%s", js)
	}
}
