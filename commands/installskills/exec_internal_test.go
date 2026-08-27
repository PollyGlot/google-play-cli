package installskills

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/redact"
)

// The pipe os/exec fabricates for a non-*os.File stderr is not just a TTY
// problem: Run does not return until EVERY descendant has closed its end. A
// grandchild that outlives the child (npx spawning a background helper) then
// hangs gplay. With the real file handed through, the fd is inherited and Run
// returns with the child.
//
// The child here backgrounds a sleeper holding stderr open, then exits at once.
func TestDefaultRun_doesNotWaitOnAGrandchildHoldingStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics")
	}
	sh, err := os.Stat("/bin/sh")
	if err != nil || sh.IsDir() {
		t.Skipf("/bin/sh unavailable: %v", err)
	}

	dir := t.TempDir()
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errFile.Close() }()
	// The child's stdout goes to a file too: left on os.Stdout, the backgrounded
	// sleeper would hold `go test`'s own capture pipe open and stall the run.
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outFile.Close() }()

	// Exactly what run() hands to the child: cobra's writer, unwrapped.
	stderr := childStream(redact.Writer(errFile))

	start := time.Now()
	runErr := defaultRun(
		context.Background(),
		"/bin/sh",
		[]string{"-c", "(sleep 3) & echo child-said-this 1>&2"},
		strings.NewReader(""),
		outFile,
		stderr,
	)
	elapsed := time.Since(start)

	if runErr != nil {
		t.Fatalf("running the child failed: %v", runErr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Run blocked %s on a grandchild holding stderr: exec substituted a pipe", elapsed)
	}
	// The child's bytes must still land in the file: handing over the fd is not
	// dropping the stream.
	got, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "child-said-this") {
		t.Errorf("the child's stderr did not reach the file: %q", got)
	}
}
