package exit

import "fmt"

type UsageError struct {
	Msg         string
	Replacement *Replacement
}

type Replacement struct {
	Kind      string
	Removed   string
	Use       string
	RemovedIn string
	Note      string
}

func (e *UsageError) Error() string { return e.Msg }

func (e *UsageError) ExitCode() int { return 2 }

func Usagef(format string, a ...any) *UsageError {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}

type ContainmentError struct{ UsageError }

func (e *ContainmentError) Unwrap() error { return &e.UsageError }

func Containmentf(format string, a ...any) *ContainmentError {
	return &ContainmentError{UsageError{Msg: fmt.Sprintf(format, a...)}}
}
