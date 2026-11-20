package exit

import "fmt"

const FindingsCode = 70

type FindingsError struct{ Msg string }

func (e *FindingsError) Error() string { return e.Msg }

func (e *FindingsError) ExitCode() int { return FindingsCode }

func Findingsf(format string, a ...any) *FindingsError {
	return &FindingsError{Msg: fmt.Sprintf(format, a...)}
}
