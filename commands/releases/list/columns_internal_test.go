package list

import (
	"strconv"
	"testing"
)

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
