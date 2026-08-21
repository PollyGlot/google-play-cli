// Package set_test exercises `gplay compliance datasafety set` at the kernel
// level. This file covers the #141 surface: the --dry-run rehearsal (validate
// + resolve package/account + report the target) which makes ZERO network
// calls and needs no --confirm, plus the implicit-validate short-circuit and
// package resolution. The live-POST path (#142) is exercised in
// set_live_test.go with a mocked transport.
package set_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	setcmd "github.com/PollyGlot/google-play-cli/commands/compliance/datasafety/set"
	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// newOfflineRC builds a RunContext with NO Account and NO transport: a
// dry-run must complete here, which proves it makes no network call and
// needs no credentials.
func newOfflineRC(t *testing.T) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{}
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

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "data-safety.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

// validCSV returns a well-formed CSV (header matches the bundled reference)
// with dataRows data rows, plus the byte length set would POST.
func validCSV(t *testing.T, dataRows int) (path string, bytesLen, rows int) {
	t.Helper()
	hdr := strings.Join(datasafety.ReferenceHeader(), ",")
	cols := len(datasafety.ReferenceHeader())
	row := strings.Repeat("x,", cols-1) + "x"
	b := strings.Builder{}
	b.WriteString(hdr + "\n")
	for i := 0; i < dataRows; i++ {
		b.WriteString(row + "\n")
	}
	content := b.String()
	return writeCSV(t, content), len(content), dataRows
}

// TestDryRun_valid_reportsTarget is the #141 tracer bullet: a valid CSV
// dry-run resolves the package, makes zero network calls, and reports the
// target: package + "would POST N bytes / N rows".
func TestDryRun_valid_reportsTarget(t *testing.T) {
	path, nbytes, rows := validCSV(t, 4)
	rc, _ := newOfflineRC(t)

	r, err := setcmd.Run(rc, setcmd.Input{
		Package: "com.example.app",
		File:    path,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("dry-run on a valid CSV must succeed offline: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on dry-run")
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := table.String()
	if !strings.Contains(out, "com.example.app") {
		t.Errorf("dry-run output %q should name the target package", out)
	}
	if !strings.Contains(strings.ToLower(out), "dry-run") {
		t.Errorf("dry-run output %q should carry a dry-run marker", out)
	}
	for _, want := range []string{"would POST", "byte", "row"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("dry-run output %q should mention %q", out, want)
		}
	}

	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(js.String(), `"dryRun": true`) {
		t.Errorf("dry-run JSON %q should be tagged dryRun:true", js.String())
	}
	if !strings.Contains(js.String(), `"bytes"`) || !strings.Contains(js.String(), `"rows"`) {
		t.Errorf("dry-run JSON %q should carry bytes and rows", js.String())
	}
	_ = nbytes
	_ = rows
}

// TestDryRun_invalidCSV_shortCircuits_exit20 asserts a structurally invalid
// CSV fails the dry-run with exit 20 before claiming any success.
func TestDryRun_invalidCSV_shortCircuits_exit20(t *testing.T) {
	hdr := strings.Join(datasafety.ReferenceHeader(), ",")
	path := writeCSV(t, hdr+"\nonly,two\n")
	rc, _ := newOfflineRC(t)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, DryRun: true})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20 (structural validation); err=%v", got, err)
	}
}

// TestDryRun_missingFile_exit20 asserts an unreadable --file fails the
// dry-run as a client-side validation error (exit 20).
func TestDryRun_missingFile_exit20(t *testing.T) {
	rc, _ := newOfflineRC(t)
	_, err := setcmd.Run(rc, setcmd.Input{
		Package: "com.example.app",
		File:    filepath.Join(t.TempDir(), "nope.csv"),
		DryRun:  true,
	})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20; err=%v", got, err)
	}
}

// TestDryRun_packageFromProjectPin asserts the package is resolved from the
// project pin when --package is omitted.
func TestDryRun_packageFromProjectPin(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rc, _ := newOfflineRC(t)
	rc.Resolved = &config.Resolved{Pin: "com.pinned.app"}

	r, err := setcmd.Run(rc, setcmd.Input{File: path, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run with pinned package: %v", err)
	}
	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(table.String(), "com.pinned.app") {
		t.Errorf("dry-run output %q should resolve the package from the project pin", table.String())
	}
}

// TestDryRun_noPackage_exit2 asserts a dry-run with neither --package nor a
// project pin is a CLI misuse (exit 2).
func TestDryRun_noPackage_exit2(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rc, _ := newOfflineRC(t)

	_, err := setcmd.Run(rc, setcmd.Input{File: path, DryRun: true})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 (no package); err=%v", got, err)
	}
}

// TestDryRun_reportsAccount asserts a resolved Account name is surfaced in
// the rehearsal (ADR-0014: dry-run resolves account/package and reports the
// target).
func TestDryRun_reportsAccount(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rc, _ := newOfflineRC(t)
	rc.AccountName = "ci-bot"

	r, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(table.String(), "ci-bot") {
		t.Errorf("dry-run output %q should report the resolved account", table.String())
	}
}

// TestNewCommand_flags asserts the #141 flag surface: --package, --file,
// --dry-run and --output exist, and the command is named "set".
func TestNewCommand_flags(t *testing.T) {
	cmd := setcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "file", "dry-run", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing expected flag --%s", name)
		}
	}
	if cmd.Use != "set" {
		t.Errorf("cmd.Use = %q, want %q", cmd.Use, "set")
	}
}
