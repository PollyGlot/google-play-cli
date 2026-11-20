package exitcodes_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	exitcodes "github.com/PollyGlot/google-play-cli/commands/help/exitcodes"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestExitCodesHelp_surfacesCode3(t *testing.T) {
	cmd := exitcodes.NewCommand()
	if cmd.Use != "exit-codes" {
		t.Fatalf("cmd.Use = %q, want exit-codes", cmd.Use)
	}
	for _, want := range []string{"3", "--grant-admin", "--confirm", "Safety flag required"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("exit-codes help should mention %q\n%s", want, cmd.Long)
		}
	}
}

func TestExitCodesHelp_surfacesTheDiagnosticCatalog(t *testing.T) {
	long := exitcodes.NewCommand().Long
	for _, d := range exit.CodeCatalog() {
		if !strings.Contains(long, string(d.Code)) {
			t.Errorf("exit-codes help omits diagnostic code %q", d.Code)
		}
	}
	for _, want := range []string{"RETRYABLE", "gplay schema --codes"} {
		if !strings.Contains(long, want) {
			t.Errorf("exit-codes help should mention %q", want)
		}
	}
	if !strings.Contains(long, exit.CodeTableString()) {
		t.Errorf("exit-codes help does not embed exit.CodeTableString verbatim\n%s", long)
	}
}

func TestExitCodesHelp_row4RendersInItsColumns(t *testing.T) {
	for _, line := range strings.Split(exitcodes.NewCommand().Long, "\n") {
		if !strings.HasPrefix(line, "4 ") {
			continue
		}
		cols := regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(line), -1)
		want := []string{
			"4",
			"Denied by environment policy (GPLAY_READONLY): a mutating command was refused",
			"no; not resolvable by a flag, change the environment",
		}
		if !slices.Equal(cols, want) {
			t.Fatalf("row 4 columns = %q, want %q", cols, want)
		}
		return
	}
	t.Fatal("exit-codes help has no row for exit 4")
}

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
