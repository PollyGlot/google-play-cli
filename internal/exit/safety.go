package exit

import "fmt"

// SafetyFlagError is the exit-3 error of docs/DESIGN.md §9: the command is
// well-formed, but a named safety-acknowledgment flag (--confirm /
// --grant-admin) is missing. It is deliberately distinct from UsageError
// (exit 2, malformed): exit 3 is *deterministically resolvable*: re-run with
// the named flag, which is the one distinction an automated caller most needs
// (ADR-0017). The Flag field names the missing flag (no leading --) so callers
// can branch structurally; the Msg states it verbatim for humans.
type SafetyFlagError struct {
	Flag string
	Msg  string
}

// Error returns the human-facing message naming the missing flag.
func (e *SafetyFlagError) Error() string { return e.Msg }

// ExitCode reports 3 (safety flag required) per docs/DESIGN.md §9.
func (e *SafetyFlagError) ExitCode() int { return 3 }

// SafetyFlag builds a *SafetyFlagError for the named flag with a printf-style
// message.
func SafetyFlag(flag, format string, a ...any) *SafetyFlagError {
	return &SafetyFlagError{Flag: flag, Msg: fmt.Sprintf(format, a...)}
}
