package exit_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// TestCatalog_columnsAreSelfContained guards the two-column split of every row.
// The em dash sweep (#444) once moved a closing parenthesis from Meaning into
// RetrySafe, so `gplay exit-codes` printed "no) change the environment" in the
// RETRY-SAFE column (#594). Each field is rendered in its own column (help
// text, surface golden, website table), so each must read on its own:
// parentheses balanced within the field, and RetrySafe opening with a verdict.
func TestCatalog_columnsAreSelfContained(t *testing.T) {
	verdicts := []string{"n/a", "no", "yes", "sometimes", "re-run"}
	for _, d := range exit.Catalog() {
		for field, text := range map[string]string{"Meaning": d.Meaning, "RetrySafe": d.RetrySafe} {
			if strings.Count(text, "(") != strings.Count(text, ")") {
				t.Errorf("exit %d: %s has unbalanced parentheses: %q", d.Code, field, text)
			}
		}
		ok := false
		for _, v := range verdicts {
			if d.RetrySafe == v || strings.HasPrefix(d.RetrySafe, v+";") || strings.HasPrefix(d.RetrySafe, v+" ") {
				ok = true
			}
		}
		if !ok {
			t.Errorf("exit %d: RetrySafe %q does not open with a verdict %v", d.Code, d.RetrySafe, verdicts)
		}
	}
}

// TestCatalog_row4 pins the corrected wording of the GPLAY_READONLY row, the
// one #594 found garbled.
func TestCatalog_row4(t *testing.T) {
	for _, d := range exit.Catalog() {
		if d.Code != 4 {
			continue
		}
		if d.Meaning != "Denied by environment policy (GPLAY_READONLY): a mutating command was refused" {
			t.Errorf("exit 4 Meaning = %q", d.Meaning)
		}
		if d.RetrySafe != "no; not resolvable by a flag, change the environment" {
			t.Errorf("exit 4 RetrySafe = %q", d.RetrySafe)
		}
		return
	}
	t.Fatal("exit.Catalog has no exit 4")
}
