package view_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	viewcmd "github.com/PollyGlot/google-play-cli/commands/appstore/catalog/view"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRun_humanViews_stripTerminalEscapes serves a catalog view whose
// third-party text (developer name, listing title and short description)
// carries an OSC title and a CSI erase-screen, and asserts the table and
// markdown views print the text without them while JSON keeps the bytes
// verbatim (ADR-0003).
func TestRun_humanViews_stripTerminalEscapes(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	body := strings.NewReplacer(
		`"Example Ltd"`, `"Example`+outputtest.HostileJSON+` Ltd"`,
		`"Exemplaire"`, `"Exemplaire`+outputtest.HostileJSON+`"`,
		`"Court"`, `"Court`+outputtest.HostileJSON+`"`,
	).Replace(appViewBody)
	r, err := viewcmd.Run(newRC(t, newFake(http.StatusOK, body)), viewcmd.Input{StorePackage: "com.store.alt", PlayPackage: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for f, got := range outputtest.RenderHuman(t, r) {
		if !strings.Contains(got, "Example Ltd") || !strings.Contains(got, "Exemplaire") {
			t.Errorf("--output %s lost the visible text:\n%s", f, got)
		}
	}
	if js := string(outputtest.RenderJSON(t, r)); !strings.Contains(js, outputtest.HostileJSON) {
		t.Errorf("json must keep the API bytes verbatim, got:\n%s", js)
	}
}
