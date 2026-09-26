package history

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzHistoryParse fuzzes the reviews-report reader, which decodes a UTF-16
// CSV fetched from the developer's GCS bucket: its cells are review text
// written by anyone on the Play Store. Two properties:
//
//   - On arbitrary bytes (raw) Parse never panics and never returns rows
//     alongside an error.
//   - Any review title and body a well-formed report carries (quotes, commas,
//     newlines, non-BMP runes) comes back byte for byte in the right field.
//     The report is built with encoding/csv and encoded like Google's export.
func FuzzHistoryParse(f *testing.F) {
	f.Add(utf16LE(sampleCSV), "Génial", "Très bonne app, 日本語 too 🎉")
	f.Add([]byte{}, "", "")
	f.Add([]byte{0xFF, 0xFE, 'a', 0, ',', 0, 'b', 0, '\n', 0}, `say "hi"`, "line one\nline two")
	f.Add([]byte{0xFE, 0xFF, 0, '"', 0, 'x'}, " leading space", `\.`)
	f.Add([]byte{0xFF, 0xFE, 0x3D, 0xD8}, "\uFEFFbom", "# not a comment")
	f.Add([]byte("plain utf-8, not utf-16\n"), ",", "\"")

	f.Fuzz(func(t *testing.T, raw []byte, title, text string) {
		if rows, err := Parse(raw); err != nil && rows != nil {
			t.Fatalf("Parse(%q) returned %d rows alongside error %v", raw, len(rows), err)
		}

		// encoding/csv folds a quoted \r\n to \n on read, and invalid UTF-8
		// cannot survive a UTF-16 round trip: neither is a Parse defect.
		if !utf8.ValidString(title) || !utf8.ValidString(text) ||
			strings.ContainsRune(title, '\r') || strings.ContainsRune(text, '\r') {
			return
		}
		var doc bytes.Buffer
		w := csv.NewWriter(&doc)
		_ = w.Write([]string{"Package Name", "Review Title", "Review Text", "Star Rating"})
		_ = w.Write([]string{"com.example.app", title, text, "5"})
		w.Flush()
		if err := w.Error(); err != nil {
			t.Fatalf("build fixture: %v", err)
		}
		rows, err := Parse(utf16LE(doc.String()))
		if err != nil {
			t.Fatalf("Parse rejected a well-formed report %q: %v", doc.String(), err)
		}
		if len(rows) != 1 {
			t.Fatalf("Parse(%q) = %d rows, want 1", doc.String(), len(rows))
		}
		r := rows[0]
		if r.ReviewTitle != title || r.ReviewText != text || r.PackageName != "com.example.app" || r.StarRating != "5" {
			t.Fatalf("round trip lost data:\ntitle %q -> %q\ntext  %q -> %q\nrow %+v", title, r.ReviewTitle, text, r.ReviewText, r)
		}
	})
}
