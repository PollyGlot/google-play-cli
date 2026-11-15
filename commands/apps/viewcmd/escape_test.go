package viewcmd_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/apps/viewcmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRun_humanViews_stripTerminalEscapes serves details and a listing whose
// contact email and title carry an OSC title and a CSI erase-screen, and
// asserts the human views print them without the sequences while JSON keeps
// the bytes verbatim (ADR-0003).
func TestRun_humanViews_stripTerminalEscapes(t *testing.T) {
	api := &viewAPI{
		editID:      "edit-info",
		details:     `{"contactEmail":"hi@example.com` + outputtest.HostileJSON + `","defaultLanguage":"en-US"}`,
		listing:     `{"language":"en-US","title":"MyApp` + outputtest.HostileJSON + `"}`,
		detailsCode: 200,
		listingCode: 200,
		iconCode:    200,
	}
	rc, _ := newRC(t, api.serve(t))
	r, err := viewcmd.Run(rc, viewcmd.Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for f, got := range outputtest.RenderHuman(t, r) {
		if !strings.Contains(got, "hi@example.com") || !strings.Contains(got, "MyApp") {
			t.Errorf("--output %s lost the visible text:\n%s", f, got)
		}
	}
	if js := string(outputtest.RenderJSON(t, r)); !strings.Contains(js, outputtest.HostileJSON) {
		t.Errorf("json must keep the API bytes verbatim, got:\n%s", js)
	}
}
