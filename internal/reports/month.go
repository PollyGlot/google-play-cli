package reports

import (
	"fmt"
	"regexp"
	"strconv"
)

var monthFlagRE = regexp.MustCompile(`^(\d{4})-(\d{2})$`)

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

func DisplayMonth(yyyymm string) string {
	if len(yyyymm) != 6 {
		return yyyymm
	}
	return yyyymm[:4] + "-" + yyyymm[4:]
}

const maxRangeMonths = 120

func MonthRange(from, to string) ([]string, error) {
	fn, err := NormalizeMonth(from)
	if err != nil {
		return nil, fmt.Errorf("invalid --since %q: want YYYY-MM (e.g. 2026-06)", from)
	}
	tn, err := NormalizeMonth(to)
	if err != nil {
		return nil, fmt.Errorf("invalid --until %q: want YYYY-MM (e.g. 2026-06)", to)
	}
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

func splitYYYYMM(yyyymm string) (year, month int) {
	year, _ = strconv.Atoi(yyyymm[:4])
	month, _ = strconv.Atoi(yyyymm[4:])
	return year, month
}
