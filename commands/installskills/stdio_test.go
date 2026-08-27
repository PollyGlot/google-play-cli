package installskills_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/installskills"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

// streamRecorder captures the streams the command hands to the child process.
type streamRecorder struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (r *streamRecorder) run(_ context.Context, _ string, _ []string, stdin io.Reader, stdout, stderr io.Writer) error {
	r.stdin, r.stdout, r.stderr = stdin, stdout, stderr
	return nil
}

// cmd/gplay wires the root's stderr through redact.Writer, so cobra's
// ErrOrStderr() is no longer an *os.File. Handing that to exec.Cmd.Stderr makes
// os/exec fabricate a pipe and a copier goroutine: the child then sees a
// non-TTY stderr next to a TTY stdout (colours and progress lost, output
// interleaved out of order), c.Run() blocks until every descendant closes the
// pipe, and the child's bytes arrive in 32 KiB blocks, which breaks redact's
// "each write is a whole message" invariant anyway. The child must get the real
// file.
func TestInstallSkills_childGetsTheRealStderrFile(t *testing.T) {
	errFile, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errFile.Close() }()
	outFile, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outFile.Close() }()

	rec := &streamRecorder{}
	cmd := installskills.NewCommand(installskills.Options{
		LookPath: fakeLookPath("/usr/bin/npx", nil),
		Run:      rec.run,
	})
	cmd.SetOut(outFile)
	cmd.SetErr(redact.Writer(errFile))
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.stderr != io.Writer(errFile) {
		t.Errorf("child stderr = %T, want the underlying *os.File so exec passes the real fd", rec.stderr)
	}
	if rec.stdout != io.Writer(outFile) {
		t.Errorf("child stdout = %T, want the *os.File", rec.stdout)
	}
}

// Unwrapping must not reach past anything else: a plain buffer (every test, and
// any future non-file stderr) is handed to the child untouched.
func TestInstallSkills_nonFileStreamsArePassedThrough(t *testing.T) {
	rec := &streamRecorder{}
	if _, _, err := execCmd(t, installskills.Options{
		LookPath: fakeLookPath("/usr/bin/npx", nil),
		Run:      rec.run,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, isFile := rec.stderr.(*os.File); isFile {
		t.Errorf("a buffer-backed stderr was replaced by a file: %T", rec.stderr)
	}
	if rec.stderr == nil {
		t.Error("the child was given no stderr at all")
	}
}
