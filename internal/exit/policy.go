package exit

import "fmt"

// PolicyError is the exit-4 error of docs/DESIGN.md §9: the command is refused
// by an environment policy (today: GPLAY_READONLY), independent of the flags
// passed. It is deliberately distinct from both UsageError (exit 2, malformed)
// and SafetyFlagError (exit 3, "re-run with the named flag"): exit 4 means the
// refusal is NOT resolvable by adding a flag: the authority boundary lives in
// the environment, not the model's flag choices. An automated caller reading
// exit 4 must change the environment (or stop), never retry with `--confirm`.
type PolicyError struct{ Msg string }

// Error returns the human-facing refusal message (which names the policy).
func (e *PolicyError) Error() string { return e.Msg }

// ExitCode reports 4 (denied by environment policy) per docs/DESIGN.md §9.
func (e *PolicyError) ExitCode() int { return 4 }

// Policyf builds a *PolicyError from a printf-style message.
func Policyf(format string, a ...any) *PolicyError {
	return &PolicyError{Msg: fmt.Sprintf(format, a...)}
}
