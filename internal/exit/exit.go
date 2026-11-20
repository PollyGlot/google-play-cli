package exit

import "errors"

type Coder interface {
	ExitCode() int
}

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
