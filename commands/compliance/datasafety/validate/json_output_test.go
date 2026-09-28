package validatecmd_test

import (
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/validate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// headerHint is the validator's one non-fatal warning, copied verbatim so the
// golden shows the real line a CI log would carry.
const headerHint = "header differs from gplay's bundled reference template: this is a hint, not an error (Google's Data Safety CSV template evolves, and only the live POST validates the declaration). Double-check that --file points at a Data Safety export."

// TestRenderJSON_clean_golden freezes a structurally valid CSV with a
// reference header: no warnings key at all (omitempty). The path carries an &
// since it echoes whatever --file the operator passed.
func TestRenderJSON_clean_golden(t *testing.T) {
	p := validatecmd.Payload{File: "./compliance/q3 & q4/data-safety.csv", OK: true, Rows: 42, Columns: 5}
	outputtest.GoldenJSON(t, "clean.json.golden", p)
}

// TestRenderJSON_withWarning_golden freezes the header-divergence hint: still
// ok:true, the warning rides along as data rather than failing the gate.
func TestRenderJSON_withWarning_golden(t *testing.T) {
	p := validatecmd.Payload{
		File:     validatecmd.DefaultFile,
		OK:       true,
		Rows:     42,
		Columns:  6,
		Warnings: []string{headerHint},
	}
	outputtest.GoldenJSON(t, "with_warning.json.golden", p)
}
