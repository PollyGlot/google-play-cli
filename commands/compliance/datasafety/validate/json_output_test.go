package validatecmd_test

import (
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/validate"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

const headerHint = "header differs from gplay's bundled reference template: this is a hint, not an error (Google's Data Safety CSV template evolves, and only the live POST validates the declaration). Double-check that --file points at a Data Safety export."

func TestRenderJSON_clean_golden(t *testing.T) {
	p := validatecmd.Payload{File: "./compliance/q3 & q4/data-safety.csv", OK: true, Rows: 42, Columns: 5}
	outputtest.GoldenJSON(t, "clean.json.golden", p)
}

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
