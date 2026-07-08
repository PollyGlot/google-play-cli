// Package history parses the monthly reviews CSV report Google deposits in the
// developer's reporting bucket — the full-history channel behind `gplay reviews
// history` beyond reviews.list's 7-day window (#94 / ADR-0037). Two facts drive
// the code: the file is UTF-16 (Google documents the BigQuery import as "convert
// from UTF-16 to UTF-8"), so a transcoding step is mandatory; and the schema is
// a stable, documented 16-column header, so parsing is header-driven (column
// order is read from the header row, never assumed).
//
// gplay hand-rolls the UTF-16 decode rather than adding golang.org/x/text: the
// reports are UTF-16LE with a BOM and the decode is a dozen lines, so a
// dependency would not earn its place (ADR-0007's spirit — no library gplay can
// do without).
package history

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// Row is one parsed review from the CSV report, modeling the 16 documented
// columns. JSON tags are stable lowerCamel names derived from the headers — the
// `--output json` shape (ADR-0037's documented deviation from the ADR-0003
// pass-through: the upstream is a CSV file, not a JSON response). Every field is
// a string: the report leaves most columns optional and gplay does not reshape
// the reviewer's data, it transcodes and surfaces it verbatim.
type Row struct {
	PackageName                    string `json:"packageName"`
	AppVersionCode                 string `json:"appVersionCode"`
	AppVersionName                 string `json:"appVersionName"`
	ReviewerLanguage               string `json:"reviewerLanguage"`
	Device                         string `json:"device"`
	ReviewSubmitDateAndTime        string `json:"reviewSubmitDateAndTime"`
	ReviewSubmitMillisSinceEpoch   string `json:"reviewSubmitMillisSinceEpoch"`
	ReviewLastUpdateDateAndTime    string `json:"reviewLastUpdateDateAndTime"`
	ReviewLastUpdateMillisSinceEpo string `json:"reviewLastUpdateMillisSinceEpoch"`
	StarRating                     string `json:"starRating"`
	ReviewTitle                    string `json:"reviewTitle"`
	ReviewText                     string `json:"reviewText"`
	DeveloperReplyDateAndTime      string `json:"developerReplyDateAndTime"`
	DeveloperReplyMillisSinceEpoch string `json:"developerReplyMillisSinceEpoch"`
	DeveloperReplyText             string `json:"developerReplyText"`
	ReviewLink                     string `json:"reviewLink"`
}

// header maps a documented CSV header label to the Row field it fills. Parsing
// is header-driven: a report that reorders, adds, or omits columns is still read
// correctly, and an unrecognized header is simply ignored rather than throwing
// the row alignment off.
var header = map[string]func(*Row, string){
	"Package Name":                          func(r *Row, v string) { r.PackageName = v },
	"App Version Code":                      func(r *Row, v string) { r.AppVersionCode = v },
	"App Version Name":                      func(r *Row, v string) { r.AppVersionName = v },
	"Reviewer Language":                     func(r *Row, v string) { r.ReviewerLanguage = v },
	"Device":                                func(r *Row, v string) { r.Device = v },
	"Review Submit Date and Time":           func(r *Row, v string) { r.ReviewSubmitDateAndTime = v },
	"Review Submit Millis Since Epoch":      func(r *Row, v string) { r.ReviewSubmitMillisSinceEpoch = v },
	"Review Last Update Date and Time":      func(r *Row, v string) { r.ReviewLastUpdateDateAndTime = v },
	"Review Last Update Millis Since Epoch": func(r *Row, v string) { r.ReviewLastUpdateMillisSinceEpo = v },
	"Star Rating":                           func(r *Row, v string) { r.StarRating = v },
	"Review Title":                          func(r *Row, v string) { r.ReviewTitle = v },
	"Review Text":                           func(r *Row, v string) { r.ReviewText = v },
	"Developer Reply Date and Time":         func(r *Row, v string) { r.DeveloperReplyDateAndTime = v },
	"Developer Reply Millis Since Epoch":    func(r *Row, v string) { r.DeveloperReplyMillisSinceEpoch = v },
	"Developer Reply Text":                  func(r *Row, v string) { r.DeveloperReplyText = v },
	"Review Link":                           func(r *Row, v string) { r.ReviewLink = v },
}

// Parse transcodes the UTF-16 report bytes to UTF-8 and decodes the CSV into
// Rows, driven by the header line. An empty report (header only, or no bytes)
// yields zero rows and no error — a month with no reviews is not a failure.
func Parse(raw []byte) ([]Row, error) {
	text := decodeUTF16(raw)
	text = strings.TrimPrefix(text, "\uFEFF") // a leftover BOM rune can survive a decode
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1 // tolerate ragged trailing-empty columns
	records, err := rd.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse reviews CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	// Build the column index from the header row: position → setter.
	setters := make([]func(*Row, string), len(records[0]))
	for i, h := range records[0] {
		setters[i] = header[strings.TrimSpace(h)] // nil for unknown columns
	}

	rows := make([]Row, 0, len(records)-1)
	for _, rec := range records[1:] {
		var row Row
		for i, cell := range rec {
			if i < len(setters) && setters[i] != nil {
				setters[i](&row, cell)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// decodeUTF16 transcodes UTF-16 bytes to a UTF-8 string. It honors a leading
// BOM (LE 0xFF 0xFE / BE 0xFE 0xFF) and defaults to little-endian when none is
// present — the encoding Google's reports use. A trailing odd byte (malformed
// tail) is dropped rather than corrupting the whole decode.
func decodeUTF16(b []byte) string {
	order := binary.ByteOrder(binary.LittleEndian)
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		order, b = binary.LittleEndian, b[2:]
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		order, b = binary.BigEndian, b[2:]
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, order.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(u16))
}

// monthRE matches a monthly reviews object name and captures its YYYYMM. The
// package name is embedded literally, so the caller anchors it per package.
func monthRE(pkg string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|/)reviews_` + regexp.QuoteMeta(pkg) + `_(\d{6})\.csv$`)
}

// ObjectName is the report key for pkg in the given YYYYMM month, under the
// bucket's "reviews/" prefix.
func ObjectName(pkg, yyyymm string) string {
	return ReviewsPrefix(pkg) + yyyymm + ".csv"
}

// ReviewsPrefix is the object-name prefix under which a package's monthly
// review reports live — the argument to a bucket listing when discovering the
// latest month.
func ReviewsPrefix(pkg string) string {
	return "reviews/reviews_" + pkg + "_"
}

// LatestMonth returns the newest YYYYMM present among names (typically a bucket
// listing) for pkg, or "" when none match. YYYYMM sorts lexicographically the
// same as chronologically, so a string max is the latest month.
func LatestMonth(pkg string, names []string) string {
	re := monthRE(pkg)
	var months []string
	for _, n := range names {
		if m := re.FindStringSubmatch(n); m != nil {
			months = append(months, m[1])
		}
	}
	if len(months) == 0 {
		return ""
	}
	sort.Strings(months)
	return months[len(months)-1]
}

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
