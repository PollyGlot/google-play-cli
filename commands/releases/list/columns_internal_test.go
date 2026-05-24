package list

import (
	"strconv"
	"testing"
)

// TestDefaultColumns_matchRegistryExactly locks the invariant the rest of
// the command relies on: every DefaultColumns key is renderable (present
// in columnRegistry, so renderers never hit a nil value func on the
// default path), and every registry key is in DefaultColumns (so the
// unknown-column error message, which lists DefaultColumns as the valid
// set, stays complete). Drift in either direction fails here at CI time.
func TestDefaultColumns_matchRegistryExactly(t *testing.T) {
	for _, k := range DefaultColumns {
		if _, ok := columnRegistry[k]; !ok {
			t.Errorf("DefaultColumns has %q with no columnRegistry entry — default render would emit a blank column", k)
		}
	}
	inDefaults := make(map[string]bool, len(DefaultColumns))
	for _, k := range DefaultColumns {
		inDefaults[k] = true
	}
	for k := range columnRegistry {
		if !inDefaults[k] {
			t.Errorf("columnRegistry has %q absent from DefaultColumns — the unknown-column error would under-report it as valid", k)
		}
	}
}

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
