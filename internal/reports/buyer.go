package reports

import (
	"fmt"
	"strings"
	"unicode"
)

// MaskBuyerLocation removes the buyer's city, state and postcode columns from
// a finance report (ADR-0050 §9) and returns the columns it hid, in header
// order; the input is left untouched. Columns are recognised from their
// header text, never by position, so masking survives a Google rename: a
// header is buyer location when one of its words is "buyer" and another a
// place below the country (city, state, province, postcode, post code,
// postal, zip). A header naming the country is never masked: revenue per
// country is the common aggregate and locates no one.
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
	out.Rows = make([]Row, len(t.Rows))
	for i, r := range t.Rows {
		kept := make(Row, len(r))
		for k, v := range r {
			if !drop[k] {
				kept[k] = v
			}
		}
		out.Rows[i] = kept
	}
	return out, hidden
}

// isBuyerLocation applies MaskBuyerLocation's header rule.
func isBuyerLocation(header string) bool {
	words := headerWords(header)
	buyer, place := false, false
	for i, w := range words {
		switch w {
		case "country":
			return false
		case "buyer", "buyers":
			buyer = true
		case "city", "state", "province", "postcode", "postal", "zip", "zipcode":
			place = true
		case "post":
			place = place || (i+1 < len(words) && words[i+1] == "code")
		}
	}
	return buyer && place
}

// headerWords are a header's runs of letters and digits, lowercased: the
// words of the key rule (keyFor), so "Buyer's Post-Code" gives buyer, s,
// post, code.
func headerWords(header string) []string {
	return strings.FieldsFunc(strings.ToLower(header), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// packageKeys are the column keys that carry the app's package name, by
// preference. Google's finance reports call it "Product ID" (the package
// name, "SKU ID" being the in-app product), so productId comes last, after
// the explicit spellings.
var packageKeys = []string{"packageName", "packageId", "productId"}

// FilterPackage keeps the rows of one app, matched on the package column the
// header declares (packageKeys), never on a fixed position. A report with no
// such column cannot be filtered: NoPackageColumnError.
func FilterPackage(t Table, pkg string) (Table, error) {
	key := ""
	for _, want := range packageKeys {
		for _, c := range t.Columns {
			if c.Key == want {
				key = want
				break
			}
		}
		if key != "" {
			break
		}
	}
	if key == "" {
		headers := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			headers[i] = c.Header
		}
		return Table{}, &NoPackageColumnError{Headers: headers}
	}
	out := Table{Columns: t.Columns}
	for _, r := range t.Rows {
		if strings.TrimSpace(r[key]) == pkg {
			out.Rows = append(out.Rows, r)
		}
	}
	return out, nil
}

// NoPackageColumnError means --package was asked of a report whose header
// names no package column. It is a usage error (exit 2): dropping --package
// reads the report.
type NoPackageColumnError struct{ Headers []string }

func (e *NoPackageColumnError) Error() string {
	return fmt.Sprintf("--package cannot filter this report: its header has no package column (Package Name, Package ID or Product ID); columns present: %s",
		strings.Join(e.Headers, ", "))
}

// ExitCode implements exit.Coder.
func (e *NoPackageColumnError) ExitCode() int { return 2 }
