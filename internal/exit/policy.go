package exit

import "fmt"

type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

func (e *PolicyError) ExitCode() int { return 4 }

func Policyf(format string, a ...any) *PolicyError {
	return &PolicyError{Msg: fmt.Sprintf(format, a...)}
}
