package view_test

import (
	"net/http"
	"strings"
	"testing"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/orders/view"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRun_humanViews_stripTerminalEscapes(t *testing.T) {
	body := strings.NewReplacer(
		`"PROCESSED"`, `"PROCESSED`+outputtest.HostileJSON+`"`,
		`"Coins"`, `"Coins`+outputtest.HostileJSON+`"`,
	).Replace(orderBody)
	r, err := viewcmd.Run(newRC(t, newFake(http.StatusOK, body)), viewcmd.Input{Package: "com.example.app", OrderIDs: []string{"GPA.1234-5678-9012-34567"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for f, got := range outputtest.RenderHuman(t, r) {
		if !strings.Contains(got, "PROCESSED") || !strings.Contains(got, "(Coins)") {
			t.Errorf("--output %s lost the visible text:\n%s", f, got)
		}
	}
	if js := string(outputtest.RenderJSON(t, r)); !strings.Contains(js, outputtest.HostileJSON) {
		t.Errorf("json must keep the API bytes verbatim, got:\n%s", js)
	}
}
