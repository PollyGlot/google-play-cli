package reports

import (
	"fmt"
	"regexp"
	"strconv"
)

// The month helpers every reader of the Reporting bucket shares: Play
// publishes one export per month, named by its YYYYMM, while the flags take
// YYYY-MM (--month, --since, --until). They moved here from `reviews history`
// (ADR-0037), which keeps its messages byte for byte.

// monthFlagRE validates the --month flag shape (YYYY-MM).
var monthFlagRE = regexp.MustCompile(`^(\d{4})-(\d{2})$`)

// NormalizeMonth converts a --month YYYY-MM value to the YYYYMM the object name
// uses, rejecting a malformed value or an out-of-range month.
func NormalizeMonth(month string) (string, error) {
	m := monthFlagRE.FindStringSubmatch(month)
	if m == nil {
		return "", fmt.Errorf("invalid --month %q: want YYYY-MM (e.g. 2026-06)", month)
	}
	if mm := m[2]; mm < "01" || mm > "12" {
		return "", fmt.Errorf("invalid --month %q: month must be 01..12", month)
	}
	return m[1] + m[2], nil
}

// DisplayMonth renders a YYYYMM month in the YYYY-MM shape the flags take,
// which is also how the JSON output names months.
func DisplayMonth(yyyymm string) string {
	if len(yyyymm) != 6 {
		return yyyymm
	}
	return yyyymm[:4] + "-" + yyyymm[4:]
}

// maxRangeMonths caps how many months a single --since/--until sweep may span. Each
// month is one sequential GCS fetch, so an unbounded range (e.g. a mistyped year
// like 1026 that still satisfies the YYYY-MM regex) would fire thousands of
// requests; the cap turns that fat-finger into a fast usage error instead. Ten
// years is far more history than Play's monthly reports ever cover.
const maxRangeMonths = 120

// MonthRange returns every YYYYMM from `from` to `to` inclusive (both given in
// the --since/--until YYYY-MM shape). It rejects a malformed bound, an inverted
// range (from after to), or a span wider than maxRangeMonths, naming the
// offending flag so the error points at what the operator typed. The result is
// chronologically ordered and, on success, never empty (from == to yields a
// single month).
func MonthRange(from, to string) ([]string, error) {
	fn, err := NormalizeMonth(from)
	if err != nil {
		return nil, fmt.Errorf("invalid --since %q: want YYYY-MM (e.g. 2026-06)", from)
	}
	tn, err := NormalizeMonth(to)
	if err != nil {
		return nil, fmt.Errorf("invalid --until %q: want YYYY-MM (e.g. 2026-06)", to)
	}
	// YYYYMM sorts lexicographically the same as chronologically.
	if fn > tn {
		return nil, fmt.Errorf("--since %s is after --until %s", from, to)
	}
	fy, fm := splitYYYYMM(fn)
	ty, tm := splitYYYYMM(tn)
	if span := (ty-fy)*12 + (tm - fm) + 1; span > maxRangeMonths {
		return nil, fmt.Errorf("range %s..%s spans %d months, more than the %d-month maximum", from, to, span, maxRangeMonths)
	}
	var out []string
	for y, mo := fy, fm; y < ty || (y == ty && mo <= tm); {
		out = append(out, fmt.Sprintf("%04d%02d", y, mo))
		if mo++; mo > 12 {
			mo, y = 1, y+1
		}
	}
	return out, nil
}

// splitYYYYMM splits a validated YYYYMM string into its year and month ints.
func splitYYYYMM(yyyymm string) (year, month int) {
	year, _ = strconv.Atoi(yyyymm[:4])
	month, _ = strconv.Atoi(yyyymm[4:])
	return year, month
}
