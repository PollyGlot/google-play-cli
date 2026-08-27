package exit

import "fmt"

// FindingsCode is the exit code of a read-only check command that ran to
// completion and REPORTED something (PRD #449, `gplay apps audit`). It is not
// an error code: nothing failed, no call was refused, the report on stdout is
// complete and valid. It exists so CI can gate on "clean account" with a bare
// exit-status test, while an automated caller can still tell "found drift"
// (70) apart from "could not look" (10/11/30/40/50).
//
// Deliberately a NEW code rather than a reuse of 1 or 60: 1 is the "nothing
// more specific fits" fallback, and 60 means a state conflict blocked the
// command. Both would make a successful audit indistinguishable from a broken
// one, which is exactly the distinction the gate needs.
const FindingsCode = 70

// FindingsError is the typed error a check command returns when its report
// carries at least one finding. The report itself is still rendered on stdout
// (the command renders it before returning this), so the error only carries
// the one-line summary the kernel prints on stderr.
type FindingsError struct{ Msg string }

// Error returns the one-line summary of what was found.
func (e *FindingsError) Error() string { return e.Msg }

// ExitCode reports 70 (findings present) per docs/DESIGN.md §9.
func (e *FindingsError) ExitCode() int { return FindingsCode }

// Findingsf builds a *FindingsError from a printf-style summary.
func Findingsf(format string, a ...any) *FindingsError {
	return &FindingsError{Msg: fmt.Sprintf(format, a...)}
}
