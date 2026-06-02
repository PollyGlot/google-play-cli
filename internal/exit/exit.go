// Package exit maps typed errors to the semantic exit codes documented in
// docs/DESIGN.md §9. Each application error implements Coder next to its
// definition; main calls exit.For once at the top of the binary so no
// hand-maintained dispatcher accumulates in cmd/gplay/main.go.
//
// Exit-code table (mirrored from docs/DESIGN.md §9):
//
//	0  — success
//	1  — generic (fallback when nothing else fits)
//	2  — CLI misuse, unknown account
//	3  — safety flag required (well-formed, but a named --confirm/--grant-admin is missing)
//	10 — auth (SA invalid, token refused, scope missing, no source)
//	11 — authorization (403 from the API)
//	20 — client-side validation
//	30 — API 4xx other than auth/perms
//	40 — API 5xx
//	50 — network failure
//	60 — state conflict (open edit, rate-limited, ambiguous target)
package exit

import "errors"

// Coder is the contract for an error that carries its own gplay exit code.
// Any package can implement it on its typed errors so that adding a new
// command does not force an edit to cmd/gplay/main.go.
type Coder interface {
	ExitCode() int
}

// For returns the exit code for err. A nil error maps to 0; an error
// (anywhere in its Unwrap chain) implementing Coder yields that code; any
// other error falls back to 1.
func For(err error) int {
	if err == nil {
		return 0
	}
	var c Coder
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	return 1
}
