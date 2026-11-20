package reports

import (
	"fmt"
	"strings"
	"unicode"
)

func MaskBuyerLocation(t Table) (Table, []Column) {
	var hidden []Column
	drop := map[string]bool{}
	out := Table{Columns: make([]Column, 0, len(t.Columns))}
	for _, c := range t.Columns {
		if isBuyerLocation(c.Header) {
			hidden = append(hidden, c)
			drop[c.Key] = true
			continue
		}
		out.Columns = append(out.Columns, c)
	}
	if len(hidden) == 0 {
		return t, nil
	}
	masked := map[*layout]*layout{}
	out.Rows = make([]Row, len(t.Rows))
	for i, r := range t.Rows {
		l, ok := masked[r.l]
		if !ok && r.l != nil {
			l = &layout{pos: make(map[string]int, len(r.l.pos)), header: r.l.header}
			for k, p := range r.l.pos {
				if !drop[k] {
					l.pos[k] = p
				}
			}
			masked[r.l] = l
		}
		out.Rows[i] = Row{l: l, cells: r.cells}
	}
	return out, hidden
}

func isBuyerLocation(header string) bool {
	words := headerWords(header)
	buyer, place := false, false
	for i, w := range words {
		if strings.Contains(w, "buyer") {
			buyer = true
			w = strings.Replace(strings.Replace(w, "buyers", "", 1), "buyer", "", 1)
			w = strings.TrimSuffix(strings.TrimPrefix(w, "of"), "of")
		}
		switch w {
		case "city", "state", "province", "postcode", "postal", "postalcode", "zip", "zipcode":
			place = true
		case "post":
			place = place || (i+1 < len(words) && words[i+1] == "code")
		}
	}
	return buyer && place
}

func headerWords(header string) []string {
	var words []string
	for _, run := range strings.FieldsFunc(header, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		rs := []rune(run)
		start := 0
		for i := 1; i < len(rs); i++ {
			lowerThenUpper := (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])) && unicode.IsUpper(rs[i])
			acronymEnd := unicode.IsUpper(rs[i-1]) && unicode.IsUpper(rs[i]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if lowerThenUpper || acronymEnd {
				words = append(words, strings.ToLower(string(rs[start:i])))
				start = i
			}
		}
		words = append(words, strings.ToLower(string(rs[start:])))
	}
	return words
}

var packageKeys = []string{"packageName", "packageId", "productId"}

func packageKey(cols []Column) (string, error) {
	for _, want := range packageKeys {
		for _, c := range cols {
			if c.Key == want {
				return want, nil
			}
		}
	}
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	return "", &NoPackageColumnError{Headers: headers}
}

type NoPackageColumnError struct{ Headers []string }

func (e *NoPackageColumnError) Error() string {
	return fmt.Sprintf("--package cannot filter this report: its header has no package column (Package Name, Package ID or Product ID); columns present: %s",
		strings.Join(e.Headers, ", "))
}

func (e *NoPackageColumnError) ExitCode() int { return 2 }
