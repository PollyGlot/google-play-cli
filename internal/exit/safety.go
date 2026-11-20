package exit

import "fmt"

type SafetyFlagError struct {
	Flag string
	Msg  string
}

func (e *SafetyFlagError) Error() string { return e.Msg }

func (e *SafetyFlagError) ExitCode() int { return 3 }

func SafetyFlag(flag, format string, a ...any) *SafetyFlagError {
	return &SafetyFlagError{Flag: flag, Msg: fmt.Sprintf(format, a...)}
}
