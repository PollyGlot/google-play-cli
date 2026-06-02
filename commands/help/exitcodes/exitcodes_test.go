package exitcodes_test

import (
	"strings"
	"testing"

	exitcodes "github.com/PollyGlot/google-play-cli/commands/help/exitcodes"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// TestExitCodesHelp_surfacesCode3 asserts `gplay help exit-codes` documents the
// new exit-3 safety-flag code naming the acknowledgment flags (#152), built
// from the single-source catalog so it cannot drift from internal/exit.
func TestExitCodesHelp_surfacesCode3(t *testing.T) {
	cmd := exitcodes.NewCommand()
	if cmd.Use != "exit-codes" {
		t.Fatalf("cmd.Use = %q, want exit-codes", cmd.Use)
	}
	for _, want := range []string{"3", "grant-admin", "--confirm", "Safety flag required"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("exit-codes help should mention %q\n%s", want, cmd.Long)
		}
	}
}

// TestCatalog_hasCode3 pins the taxonomy: code 3 is present with the
// deterministic-retry qualifier.
func TestCatalog_hasCode3(t *testing.T) {
	var found bool
	for _, d := range exit.Catalog() {
		if d.Code == 3 {
			found = true
			if !strings.Contains(d.RetrySafe, "re-run") {
				t.Errorf("code 3 retry-safe = %q, want a re-run qualifier", d.RetrySafe)
			}
		}
	}
	if !found {
		t.Error("exit.Catalog must include code 3")
	}
}
