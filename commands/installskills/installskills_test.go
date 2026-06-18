package installskills_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/installskills"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// recorder captures the npx invocation the command would run, so tests assert
// the exact recipe without a real npx or network (ADR-0028: mock exec/LookPath).
type recorder struct {
	ran     bool
	npxPath string
	args    []string
}

func (r *recorder) run(_ context.Context, npxPath string, args []string, _ io.Reader, _, _ io.Writer) error {
	r.ran = true
	r.npxPath = npxPath
	r.args = args
	return nil
}

// fakeLookPath builds a LookPath that resolves npx to path (or returns err).
func fakeLookPath(path string, err error) func(string) (string, error) {
	return func(string) (string, error) { return path, err }
}

// exec drives the command end-to-end through cobra with the given passthrough
// args, returning whatever it wrote to stdout / stderr and the run error.
func execCmd(t *testing.T, opts installskills.Options, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := installskills.NewCommand(opts)
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// Pass a non-nil slice even with no passthrough args: cobra falls back to
	// os.Args[1:] when c.args is nil, which would leak `go test` flags into the
	// command (and, with DisableFlagParsing, into the npx argv we assert on).
	cmd.SetArgs(append([]string{}, args...))
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

// wantRecipe is the opinionated, non-interactive npx argv from ADR-0028 §2.
var wantRecipe = []string{
	"--yes", "skills", "add", "PollyGlot/google-play-cli-skills",
	"--global", "--agent", "*", "--yes",
}

func TestInstallSkills_runsRecipe(t *testing.T) {
	rec := &recorder{}
	opts := installskills.Options{LookPath: fakeLookPath("/usr/bin/npx", nil), Run: rec.run}

	_, _, err := execCmd(t, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rec.ran {
		t.Fatal("expected npx to be invoked")
	}
	if rec.npxPath != "/usr/bin/npx" {
		t.Errorf("npxPath = %q, want the resolved npx path", rec.npxPath)
	}
	if strings.Join(rec.args, " ") != strings.Join(wantRecipe, " ") {
		t.Errorf("argv = %v\nwant   %v", rec.args, wantRecipe)
	}
}

func TestInstallSkills_passthrough(t *testing.T) {
	rec := &recorder{}
	opts := installskills.Options{LookPath: fakeLookPath("/usr/bin/npx", nil), Run: rec.run}

	// Overrides documented in ADR-0028 §2 must reach `npx skills add` verbatim,
	// appended after the recipe.
	_, _, err := execCmd(t, opts, "--agent", "claude", "--project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := append(append([]string{}, wantRecipe...), "--agent", "claude", "--project")
	if strings.Join(rec.args, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v\nwant   %v", rec.args, want)
	}
}

func TestInstallSkills_npxAbsent(t *testing.T) {
	rec := &recorder{}
	opts := installskills.Options{
		// What a real exec.LookPath returns when npx is not on PATH: an
		// *exec.Error wrapping exec.ErrNotFound.
		LookPath: fakeLookPath("", &exec.Error{Name: "npx", Err: exec.ErrNotFound}),
		Run:      rec.run,
	}

	_, stderr, err := execCmd(t, opts)
	if err == nil {
		t.Fatal("expected an error when npx is absent")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1 (generic fallback, ADR-0028 §4)", got)
	}
	if rec.ran {
		t.Error("npx must not be invoked when it is absent")
	}
	// The stderr message must be actionable: name Node, print the manual recipe,
	// and point at the browse URL (ADR-0028 §4).
	for _, want := range []string{
		"Node",
		"npx skills add PollyGlot/google-play-cli-skills --global --agent '*' --yes",
		"https://github.com/PollyGlot/google-play-cli-skills",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\ngot:\n%s", want, stderr)
		}
	}
}

func TestInstallSkills_npxLookupError(t *testing.T) {
	rec := &recorder{}
	// A non-"not found" lookup failure (e.g. npx present but not executable).
	// The "install Node.js" recipe would be misleading here, so it must be
	// suppressed and the real cause surfaced instead.
	opts := installskills.Options{
		LookPath: fakeLookPath("", errors.New("permission denied")),
		Run:      rec.run,
	}

	_, stderr, err := execCmd(t, opts)
	if err == nil {
		t.Fatal("expected an error on a lookup failure")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if rec.ran {
		t.Error("npx must not be invoked on a lookup failure")
	}
	if strings.Contains(stderr, "requires Node.js") {
		t.Errorf("must not print the 'not found' recipe for a non-ErrNotFound failure\ngot:\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should surface the real cause, got: %v", err)
	}
}

// exitErr mimics *exec.ExitError: it carries the child process's exit code via
// an ExitCode() method (os.ProcessState promotes one). exit.Coder has the same
// shape, so a naive %w-wrap of the real npx error would leak the child's code
// as gplay's — the regression this test pins.
type exitErr struct{ code int }

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e exitErr) ExitCode() int { return e.code }

func TestInstallSkills_npxFailure(t *testing.T) {
	// A non-1 child exit code (7) makes the leak observable: ADR-0028 §4 says a
	// failed npx run surfaces as gplay exit 1 (opaque), never the child's code.
	failing := func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		return exitErr{code: 7}
	}
	opts := installskills.Options{LookPath: fakeLookPath("/usr/bin/npx", nil), Run: failing}

	_, _, err := execCmd(t, opts)
	if err == nil {
		t.Fatal("expected an error when npx fails")
	}
	if got := exit.For(err); got != 1 {
		t.Errorf("exit code = %d, want 1 (npx failures are opaque, ADR-0028 §4)", got)
	}
}

func TestInstallSkills_helpDoesNotRunNpx(t *testing.T) {
	rec := &recorder{}
	opts := installskills.Options{LookPath: fakeLookPath("/usr/bin/npx", nil), Run: rec.run}

	stdout, _, err := execCmd(t, opts, "--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.ran {
		t.Error("--help must not shell out to npx")
	}
	if !strings.Contains(stdout, "Node") {
		t.Errorf("help text should state the Node/npx requirement\ngot:\n%s", stdout)
	}
}

func TestInstallSkills_metadata(t *testing.T) {
	cmd := installskills.NewCommand(installskills.Options{})
	if cmd.Use != "install-skills" {
		t.Errorf("cmd.Use = %q, want install-skills", cmd.Use)
	}
	// --help must state the Node/npx dependency (ADR-0028 §3 acceptance).
	for _, want := range []string{"Node", "npx"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long should mention %q\n%s", want, cmd.Long)
		}
	}
}
