// Package validatecmd_test exercises `gplay compliance datasafety validate`
// end to end at the kernel level, and, crucially, OFFLINE. The RunContext
// is built with NO Account and NO injected transport: any attempt to
// authenticate or make an HTTP call would fail (there is no credential and
// no RoundTripper). A passing suite is itself proof that validate never
// touches the network or needs credentials.
package validatecmd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	validatecmd "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/validate"
	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// newRC builds an offline RunContext: no Account, no transport.
func newRC(t *testing.T) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Format: output.FormatJSON})
	return rc, &stdout
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

// writeCSV writes content to a fresh temp file and returns its path.
func writeCSV(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "data-safety.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

// referenceCSV builds a well-formed CSV whose header matches the bundled
// reference template (so no header-divergence warning fires) with n rows.
func referenceCSV(t *testing.T, dataRows int) string {
	t.Helper()
	hdr := strings.Join(datasafety.ReferenceHeader(), ",")
	cols := len(datasafety.ReferenceHeader())
	row := strings.Repeat("x,", cols-1) + "x"
	b := strings.Builder{}
	b.WriteString(hdr + "\n")
	for i := 0; i < dataRows; i++ {
		b.WriteString(row + "\n")
	}
	return b.String()
}

// TestRun_valid_success is the tracer bullet: a well-formed CSV validates
// with no credentials and renders a Payload; --output json reports ok:true
// and the row tally.
func TestRun_valid_success(t *testing.T) {
	path := writeCSV(t, referenceCSV(t, 3))
	rc, _ := newRC(t)

	r, err := validatecmd.Run(rc, validatecmd.Input{File: path})
	if err != nil {
		t.Fatalf("Run on valid CSV: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on success")
	}

	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(out.String(), `"ok": true`) {
		t.Errorf("success JSON %q should report ok:true", out.String())
	}
	if !strings.Contains(out.String(), `"rows": 3`) {
		t.Errorf("success JSON %q should report rows:3", out.String())
	}
}

// TestRun_emptyFile_exit20 asserts an empty CSV is a structural failure
// mapped to exit 20.
func TestRun_emptyFile_exit20(t *testing.T) {
	path := writeCSV(t, "")
	rc, _ := newRC(t)

	_, err := validatecmd.Run(rc, validatecmd.Input{File: path})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
}

// TestRun_missingFile_exit20 asserts a --file pointing at a nonexistent path
// fails as a client-side validation error (exit 20), not a panic.
func TestRun_missingFile_exit20(t *testing.T) {
	rc, _ := newRC(t)

	_, err := validatecmd.Run(rc, validatecmd.Input{File: filepath.Join(t.TempDir(), "nope.csv")})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
}

// TestRun_raggedRows_exit20 asserts a non-rectangular CSV fails with exit 20.
func TestRun_raggedRows_exit20(t *testing.T) {
	hdr := strings.Join(datasafety.ReferenceHeader(), ",")
	path := writeCSV(t, hdr+"\nonly,two\n")
	rc, _ := newRC(t)

	_, err := validatecmd.Run(rc, validatecmd.Input{File: path})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
}

// TestRun_headerMismatch_warns_exit0 asserts a divergent header does NOT
// fail the command (exit 0) but surfaces a non-fatal warning in the output.
func TestRun_headerMismatch_warns_exit0(t *testing.T) {
	path := writeCSV(t, "totally,different,header\na,b,c\n")
	rc, _ := newRC(t)

	r, err := validatecmd.Run(rc, validatecmd.Input{File: path})
	if err != nil {
		t.Fatalf("a divergent header must not fail the command, got: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "hint") &&
		!strings.Contains(strings.ToLower(out.String()), "warn") {
		t.Errorf("table output %q should surface the non-fatal header warning", out.String())
	}
}

// TestRun_default_table_rendersOK asserts the human table view names the
// file and reports the row/column tally on success.
func TestRun_default_table_rendersOK(t *testing.T) {
	path := writeCSV(t, referenceCSV(t, 2))
	rc, _ := newRC(t)

	r, err := validatecmd.Run(rc, validatecmd.Input{File: path})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("table %q should name the validated file", out.String())
	}
}

// TestNewCommand_registersFlags is a thin smoke test of the cobra wiring:
// --file and --output exist, there is NO --package (validate is file-only),
// and the command is named "validate".
func TestNewCommand_registersFlags(t *testing.T) {
	cmd := validatecmd.NewCommand(kernel.Boot{})
	if cmd.Flags().Lookup("file") == nil {
		t.Error("missing --file flag")
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("missing --output flag")
	}
	if cmd.Flags().Lookup("package") != nil {
		t.Error("validate must NOT have a --package flag (it is offline, file-only)")
	}
	if cmd.Use != "validate" {
		t.Errorf("cmd.Use = %q, want %q", cmd.Use, "validate")
	}
}
