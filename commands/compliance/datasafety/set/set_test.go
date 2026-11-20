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

func TestDryRun_invalidCSV_shortCircuits_exit20(t *testing.T) {
	hdr := strings.Join(datasafety.ReferenceHeader(), ",")
	path := writeCSV(t, hdr+"\nonly,two\n")
	rc, _ := newOfflineRC(t)

	_, err := setcmd.Run(rc, setcmd.Input{Package: "com.example.app", File: path, DryRun: true})
	if got := exitCodeOf(t, err); got != 20 {
		t.Errorf("exit = %d, want 20 (structural validation); err=%v", got, err)
	}
}

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

func TestDryRun_noPackage_exit2(t *testing.T) {
	path, _, _ := validCSV(t, 1)
	rc, _ := newOfflineRC(t)

	_, err := setcmd.Run(rc, setcmd.Input{File: path, DryRun: true})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 (no package); err=%v", got, err)
	}
}

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
