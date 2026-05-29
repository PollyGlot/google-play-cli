// Package filter implements the client-side `--stars` selector for
// `gplay reviews list`. The Google Play reviews API has no server-side
// rating filter (docs/DESIGN.md §5), so gplay applies it after the call.
// The package is pure — no IO, no exit codes — so the grammar is exhaustively
// table-tested here and the command layer maps a parse failure to exit 2.
package filter

import (
	"fmt"
	"strconv"
	"strings"
)

// Hint is the one-line grammar reminder the command appends to a parse
// failure so the operator sees the accepted shapes without consulting --help.
const Hint = `--stars accepts a single rating (1), an inclusive range (1-2), or a set (1,3,5); each rating must be 1..5`

// minStar and maxStar bound the valid Google Play rating scale.
const (
	minStar = 1
	maxStar = 5
)

// Selector is a parsed `--stars` filter: the set of star ratings (1..5) it
// admits. The zero value admits every rating, which is the "no --stars
// given" behavior — the command can call Matches unconditionally.
type Selector struct {
	allowed map[int]bool
}

// Matches reports whether stars is admitted by the selector. The zero
// Selector (no filter) admits everything.
func (s Selector) Matches(stars int) bool {
	if s.allowed == nil {
		return true
	}
	return s.allowed[stars]
}

// Parse turns a `--stars` spec into a Selector. Accepted forms: a single
// star ("1"), an inclusive range ("1-2"), and a comma set ("1,3,5"). An
// empty spec yields the match-all zero Selector. Every rating must fall in
// 1..5 and a range must run low→high; anything else is an error the command
// surfaces as exit 2.
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

// star parses one rating token and bounds-checks it to 1..5.
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
