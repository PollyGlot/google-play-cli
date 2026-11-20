package filter

import (
	"fmt"
	"strconv"
	"strings"
)

const Hint = `--stars accepts a single rating (1), an inclusive range (1-2), or a set (1,3,5); each rating must be 1..5`

const (
	minStar = 1
	maxStar = 5
)

type Selector struct {
	allowed map[int]bool
}

func (s Selector) Matches(stars int) bool {
	if s.allowed == nil {
		return true
	}
	return s.allowed[stars]
}

func Parse(spec string) (Selector, error) {
	if strings.TrimSpace(spec) == "" {
		return Selector{}, nil
	}
	allowed := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		member := strings.TrimSpace(part)
		if member == "" {
			return Selector{}, fmt.Errorf("invalid --stars %q: empty rating", spec)
		}
		lo, hi, isRange := strings.Cut(member, "-")
		if isRange {
			a, err := star(lo)
			if err != nil {
				return Selector{}, fmt.Errorf("invalid --stars %q: %w", spec, err)
			}
			b, err := star(hi)
			if err != nil {
				return Selector{}, fmt.Errorf("invalid --stars %q: %w", spec, err)
			}
			if a > b {
				return Selector{}, fmt.Errorf("invalid --stars %q: range %d-%d runs high→low", spec, a, b)
			}
			for s := a; s <= b; s++ {
				allowed[s] = true
			}
			continue
		}
		n, err := star(member)
		if err != nil {
			return Selector{}, fmt.Errorf("invalid --stars %q: %w", spec, err)
		}
		allowed[n] = true
	}
	return Selector{allowed: allowed}, nil
}

func star(tok string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(tok))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", strings.TrimSpace(tok))
	}
	if n < minStar || n > maxStar {
		return 0, fmt.Errorf("rating %d out of range (must be %d..%d)", n, minStar, maxStar)
	}
	return n, nil
}
