package kernel

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Logger is the stderr funnel (docs/DESIGN.md §8: stdout carries data, stderr
// carries logs). Every line a command writes to stderr goes through one of its
// methods, each naming the line's level, so a future --quiet (§8 promises it:
// "only errors on stderr") is one switch here instead of a hunt through every
// command. funnel_test.go fails on a direct stderr write anywhere under
// commands/.
//
// A command that runs through kernel.Run reaches it via the RunContext methods
// of the same names; the few that never build a RunContext (`gplay init`,
// `install-skills`: no Account, no API) take LoggerFor(cmd).
//
// Every write is best-effort (the exit code and the stdout payload are the
// authoritative signals) and a nil writer is a no-op rather than a panic. Each
// method appends the newline, so callers pass the body only.
type Logger struct {
	w io.Writer
}

// LoggerFor returns the funnel over cmd's stderr. main.go sets the root's
// error writer to the redacting stderr, so the lines stay redacted.
func LoggerFor(cmd *cobra.Command) Logger {
	return Logger{w: cmd.ErrOrStderr()}
}

func (rc *RunContext) log() Logger { return Logger{w: rc.Stderr} }

func (l Logger) emit(prefix, format string, args ...any) {
	if l.w == nil {
		return
	}
	_, _ = fmt.Fprintf(l.w, prefix+format+"\n", args...)
}

// Confirmf writes the committed-success line, prefixed `✓ ` (see
// RunContext.Confirmf for the rules: never on a --dry-run).
func (l Logger) Confirmf(format string, args ...any) { l.emit("✓ ", format, args...) }

// Warnf writes a non-fatal advisory, prefixed `warning: `: the one warning
// prefix of the CLI.
func (l Logger) Warnf(format string, args ...any) { l.emit("warning: ", format, args...) }

// Notef writes an informational hint, prefixed `NOTE: `: the resume token of a
// cursor listing, a freshness caveat, a pointer to a better command. Unlike
// Warnf nothing is wrong; the caller only learns where more is.
func (l Logger) Notef(format string, args ...any) { l.emit("NOTE: ", format, args...) }

// Logf writes a plain progress or result line with no marker: a per-item
// outcome in a batch (`OK <id>`), a summary count, a follow-up hint under a ✓.
// The text is the caller's, verbatim.
func (l Logger) Logf(format string, args ...any) { l.emit("", format, args...) }

// Failf writes an error-level line for one item of a batch that otherwise
// carries on (`✗ <pkg>: ...`, `ERR <id> ...`), verbatim. It is the level a
// --quiet must keep: the command's own failure still reaches stderr through
// main's single `gplay: ...` line, this is for failures the exit code
// summarises but does not name.
func (l Logger) Failf(format string, args ...any) { l.emit("", format, args...) }

// Notef writes a `NOTE: ` hint through the stderr funnel (see Logger.Notef).
func (rc *RunContext) Notef(format string, args ...any) { rc.log().Notef(format, args...) }

// Logf writes a plain progress or result line through the stderr funnel (see
// Logger.Logf).
func (rc *RunContext) Logf(format string, args ...any) { rc.log().Logf(format, args...) }

// Failf writes an error-level batch line through the stderr funnel (see
// Logger.Failf).
func (rc *RunContext) Failf(format string, args ...any) { rc.log().Failf(format, args...) }
