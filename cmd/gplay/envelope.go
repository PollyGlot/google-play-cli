package main

import (
	"io"
	"os"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

// stdoutTally forwards every write to the process stdout while counting the
// bytes, so main can tell whether a failing command already wrote something
// (its own output, or the kernel's error envelope). It forwards Fd too, which
// is what keeps TTY detection (output.Resolve) seeing the real terminal.
//
// It deliberately does not embed *os.File: an embedded WriteString would let
// io.WriteString bypass the count.
type stdoutTally struct {
	f *os.File
	n int64
}

func (t *stdoutTally) Write(p []byte) (int, error) {
	n, err := t.f.Write(p)
	t.n += int64(n)
	return n, err
}

// Fd returns the descriptor of the wrapped file (see output.IsTerminalFn).
func (t *stdoutTally) Fd() uintptr { return t.f.Fd() }

// writeFailureEnvelope is the backstop behind the kernel's JSON error envelope
// (ADR-0023). The kernel writes the envelope for a failure it sees, from inside
// RunE. A failure raised BEFORE RunE never reaches it: an unknown or repeated
// flag, a wrong number of positional arguments, an unknown subcommand. Those
// returned with stdout empty even under --output json (#593), so an agent that
// branches on the envelope saw nothing for the very mistakes it most often
// makes.
//
// So main calls this once Execute has failed. It writes the envelope when
// nothing reached stdout yet (never a second object, never on top of a
// command's own output, the rule the kernel follows too) and when the format
// resolves to JSON. The format is read from argv rather than from the parsed
// flag, because pflag stops at the first bad token and may never have reached
// --output; then comes the same GPLAY_DEFAULT_OUTPUT / CI / TTY cascade as
// every command. An --output value that is itself invalid resolves to an error,
// and the failure stays a plain stderr line: the one case ADR-0023 still exempts.
func writeFailureEnvelope(stdout io.Writer, wrote bool, argv []string, err error) {
	if err == nil || wrote {
		return
	}
	format, ferr := output.Resolve(output.Format(outputFlagValue(argv)), stdout)
	if ferr != nil || format != output.FormatJSON {
		return
	}
	// Best-effort like the kernel's: stderr and the exit code stay the
	// authoritative failure signal.
	_ = output.WriteErrorEnvelope(stdout, err)
}

// outputFlagValue returns the value of the first --output in argv (`--output
// json` or `--output=json`), or "" when there is none. Scanning stops at "--",
// after which every token is a positional argument. The first occurrence wins
// because a repeated --output is itself rejected, and the first is the value
// the parser had already accepted.
func outputFlagValue(argv []string) string {
	for i, a := range argv {
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--output="); ok {
			return v
		}
		if a == "--output" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}
