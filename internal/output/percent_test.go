package output_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

// TestPercent renders a 0..1 rollout fraction as a trimmed percentage,
// sidestepping the float noise of a naive f*100 (0.1*100 == 10.000000000000002).
func TestPercent(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.1, "10%"},
		{0.05, "5%"}, // float-noise case: 0.05*100 != 5 exactly
		{0.005, "0.5%"},
		{0.25, "25%"},
		{1.0, "100%"},
		{0, "0%"},
	}
	for _, c := range cases {
		if got := output.Percent(c.in); got != c.want {
			t.Errorf("Percent(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
