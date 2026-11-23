package exit

import "errors"

type Coder interface {
	ExitCode() int
}

func For(err error) int {
	if err == nil {
		return 0
	}
	var escape *ContainmentError
	if errors.As(err, &escape) {
		return escape.ExitCode()
	}
	var c Coder
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	return 1
}
