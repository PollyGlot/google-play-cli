package output

import (
	"strconv"
	"strings"
)

// Percent renders a 0..1 fraction as a trimmed percentage: 0.1 -> "10%",
// 0.005 -> "0.5%". It formats at four decimal places (covering Play's rollout
// granularity) then trims trailing zeros, sidestepping the float noise of a
// naive f*100 (0.1*100 == 10.000000000000002). Used for the userFraction in
// rollout views and ✓ confirmation lines (DESIGN §8).
func Percent(f float64) string {
	s := strconv.FormatFloat(f*100, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + "%"
}
