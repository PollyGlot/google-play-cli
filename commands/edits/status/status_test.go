package status_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	statuscmd "github.com/PollyGlot/google-play-cli/commands/edits/status"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const pkg = "com.example.app"

// newRC builds a network-free RunContext (status is a local read) pinned to a
// project whose .gplay/ is a fresh temp dir.
func newRC(t *testing.T) (*kernel.RunContext, string) {
	t.Helper()
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	rc := kernel.NewForTest(context.Background(), kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir
}

func render(t *testing.T, r output.Renderable, f output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, f, r.Renderers()); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestRun_open_reportsEditID(t *testing.T) {
	rc, gplayDir := newRC(t)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-55"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	r, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "edit-55") {
		t.Errorf("table = %q, want it to name the open edit", out)
	}
	if out := render(t, r, output.FormatJSON); !strings.Contains(out, `"open": true`) || !strings.Contains(out, `"editId": "edit-55"`) {
		t.Errorf("json = %q", out)
	}
}

func TestRun_none_reportsNoOpenEdit(t *testing.T) {
	rc, _ := newRC(t)

	r, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out := render(t, r, output.FormatTable); !strings.Contains(out, "no open explicit edit") {
		t.Errorf("table = %q, want the no-open-edit message", out)
	}
	if out := render(t, r, output.FormatJSON); !strings.Contains(out, `"open": false`) {
		t.Errorf("json = %q, want open:false", out)
	}
}

func TestRun_noProject_exit2(t *testing.T) {
	rc, _ := newRC(t)
	rc.Resolved = &config.Resolved{Pin: pkg} // no ProjectSharedPath

	_, err := statuscmd.Run(rc, statuscmd.Input{Package: pkg})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Fatalf("err = %v, want exit 2 (no project)", err)
	}
}
