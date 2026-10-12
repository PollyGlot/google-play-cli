package exit

import "fmt"

// UsageError is the shared CLI-misuse error every command returns for the
// exit-2 cases in docs/DESIGN.md §9 (unknown flag, bad value, missing
// required arg, unknown --columns key, …). Promoting it out of each
// command's verbatim local copy keeps the exit-2 contract in one place:
// the code lives here, so a command can no longer drift from the documented
// meaning of "CLI misuse".
//
// It satisfies Coder (ExitCode()=2), so exit.For maps it, and any error
// that wraps it: to 2 without a dispatcher edit.
//
// Replacement is set when the misuse is a call to surface gplay removed (a
// renamed verb or flag, ADR-0049): the error stays exit 2, and the JSON error
// envelope reads the replacement from here with errors.As. Nil otherwise.
type UsageError struct {
	Msg         string
	Replacement *Replacement
}

// Replacement names what replaced a removed verb or flag (ADR-0049). Kind is
// "command" or "flag". For a command, Removed and Use are full command paths
// starting with "gplay"; for a flag, flag names on the same command. RemovedIn
// is the gplay version that removed it.
type Replacement struct {
	Kind      string
	Removed   string
	Use       string
	RemovedIn string
}

// Error returns the misuse message.
func (e *UsageError) Error() string { return e.Msg }

// ExitCode reports 2 (CLI misuse) per docs/DESIGN.md §9.
func (e *UsageError) ExitCode() int { return 2 }

// Usagef builds a *UsageError from a printf-style message: the ergonomic
// form for the many call sites that compose a misuse message with a
// dynamic value (an offending flag, the valid set, …).
func Usagef(format string, a ...any) *UsageError {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}
