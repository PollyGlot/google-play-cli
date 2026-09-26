package exitcodes_test

import (
	"regexp"
	"slices"
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
	for _, want := range []string{"3", "--grant-admin", "--confirm", "Safety flag required"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("exit-codes help should mention %q\n%s", want, cmd.Long)
		}
	}
}

// TestExitCodesHelp_surfacesTheDiagnosticCatalog is the CLI-introspection half
// of slice #454: every diagnostic code must be readable from `gplay help
// exit-codes`, so a skill author never opens the source to learn the vocabulary.
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
	// Embeds the shared renderer verbatim rather than a local copy of the
	// column layout: this is what keeps the help topic and `schema --codes`
	// from drifting apart.
	if !strings.Contains(long, exit.CodeTableString()) {
		t.Errorf("exit-codes help does not embed exit.CodeTableString verbatim\n%s", long)
	}
}

// TestExitCodesHelp_row4RendersInItsColumns reads the printed table the way a
// person does, by column: #594 found row 4's RETRY-SAFE cell reading
// "no) change the environment" because a parenthesis had slid across the split.
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
