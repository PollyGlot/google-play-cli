package list

import (
	"strconv"
	"testing"
)

// The DefaultColumns↔registry invariant these list commands used to assert
// per-command is now structural in output.ColumnSet (a single ordered list
// cannot drift from itself) and tested once in internal/output. What stays
// command-specific is the cell formatting below.

func TestFormatFraction_decimalNeverScientific(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1.0, "1"},
		{0.1, "0.1"},
		{0.5, "0.5"},
		{0.05, "0.05"},
		{0.001, "0.001"},
	}
	for _, c := range cases {
		if got := formatFraction(c.in); got != c.want {
			t.Errorf("formatFraction(%s) = %q, want %q", strconv.FormatFloat(c.in, 'f', -1, 64), got, c.want)
		}
	}
}
