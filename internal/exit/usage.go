package exit

import "fmt"

// UsageError is the shared CLI-misuse error every command returns for the
// exit-2 cases in docs/DESIGN.md §9 (unknown flag, bad value, missing
// required arg, unknown --columns key, …). Promoting it out of each
// command's verbatim local copy keeps the exit-2 contract in one place:
// the code lives here, so a command can no longer drift from the documented
// meaning of "CLI misuse".
//
// It satisfies Coder (ExitCode()=2), so exit.For maps it — and any error
// that wraps it — to 2 without a dispatcher edit.
type UsageError struct{ Msg string }

// Error returns the misuse message.
func (e *UsageError) Error() string { return e.Msg }

// ExitCode reports 2 (CLI misuse) per docs/DESIGN.md §9.
func (e *UsageError) ExitCode() int { return 2 }

// Usagef builds a *UsageError from a printf-style message — the ergonomic
// form for the many call sites that compose a misuse message with a
// dynamic value (an offending flag, the valid set, …).
func Usagef(format string, a ...any) *UsageError {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}
