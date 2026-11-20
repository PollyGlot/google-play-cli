package output

import (
	"strconv"
	"strings"
)

func Percent(f float64) string {
	s := strconv.FormatFloat(f*100, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + "%"
}
