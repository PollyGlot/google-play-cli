// Package outputtest is the test-only seam for internal/output. It hides
// the SetIsTerminalFunc/IsTerminalFunc swap-and-restore dance behind a
// single ForceTerminal helper so command-level tests no longer copy it.
//
// Production code must not import this package.
package outputtest

import (
	"io"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

// ForceTerminal pins output.isTTY to v for the duration of t, restoring
// the previous detector via t.Cleanup. Use it instead of attaching a pty
// to the test process.
func ForceTerminal(t *testing.T, v bool) {
	t.Helper()
	prev := output.IsTerminalFunc()
	output.SetIsTerminalFunc(func(_ io.Writer) bool { return v })
	t.Cleanup(func() { output.SetIsTerminalFunc(prev) })
}
