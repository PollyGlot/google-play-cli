package reports

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzDecode fuzzes the Bulk report decoder, which reads files fetched from a
// bucket gplay does not control. Three properties:
//
//   - On arbitrary bytes Decode never panics, never returns rows alongside an
//     error, and every row key it returns is a declared column.
//   - Any cell value and header a well-formed report carries (quotes, commas,
//     newlines, non-BMP runes) comes back byte for byte, whether the file is
//     UTF-16 LE with a BOM, UTF-16 BE with a BOM, or UTF-8.
//   - The key derived from a header is the same whatever the encoding.
func FuzzDecode(f *testing.F) {
	f.Add(encodeUTF16(installsCSV, binary.LittleEndian, true), "Daily Device Installs", "00042")
	f.Add([]byte{}, "", "")
	f.Add([]byte{0xFF, 0xFE, 'a', 0, ',', 0, 'b', 0, '\n', 0}, `say "hi"`, "line one\nline two")
	f.Add([]byte{0xFE, 0xFF, 0, '"', 0, 'x'}, " leading space", `\.`)
	f.Add([]byte{0xFF, 0xFE, 0x3D, 0xD8}, "\uFEFFbom", "# not a comment")
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'a', ',', 'a', '\n', '1'}, "Größe (€)", "🎉, 日本語")
	f.Add([]byte("x\x00y\n"), "%", "")

	f.Fuzz(func(t *testing.T, raw []byte, header, value string) {
		tbl, err := Decode(raw)
		if err != nil && (tbl.Rows != nil || tbl.Columns != nil) {
			t.Fatalf("Decode(%q) returned a table alongside error %v", raw, err)
		}
		declared := map[string]bool{}
		for _, c := range tbl.Columns {
			if declared[c.Key] {
				t.Fatalf("Decode(%q) declared key %q twice", raw, c.Key)
			}
			declared[c.Key] = true
		}
		for _, r := range tbl.Rows {
			for k := range r {
				if !declared[k] {
					t.Fatalf("Decode(%q) row key %q is not a column", raw, k)
				}
			}
		}

		// encoding/csv folds a quoted \r\n to \n on read, invalid UTF-8 cannot
		// survive a UTF-16 round trip, and a header's surrounding space and a
		// leading BOM rune are not part of its text: none is a Decode defect.
		if !utf8.ValidString(header) || !utf8.ValidString(value) ||
			strings.ContainsRune(header, '\r') || strings.ContainsRune(value, '\r') ||
			strings.TrimSpace(header) != header || strings.HasPrefix(header, "\uFEFF") ||
			strings.ContainsRune(header, 0) || strings.ContainsRune(value, 0) {
			return
		}
		var doc bytes.Buffer
		w := csv.NewWriter(&doc)
		_ = w.Write([]string{"Date", header})
		_ = w.Write([]string{"2026-09-01", value})
		w.Flush()
		if err := w.Error(); err != nil {
			t.Fatalf("build fixture: %v", err)
		}
		var key string
		for i, enc := range [][]byte{
			encodeUTF16(doc.String(), binary.LittleEndian, true),
			encodeUTF16(doc.String(), binary.BigEndian, true),
			doc.Bytes(),
		} {
			tbl, err := Decode(enc)
			if err != nil {
				t.Fatalf("encoding %d: Decode rejected a well-formed report %q: %v", i, doc.String(), err)
			}
			if len(tbl.Columns) != 2 || len(tbl.Rows) != 1 {
				t.Fatalf("encoding %d: Decode(%q) = %+v", i, doc.String(), tbl)
			}
			c := tbl.Columns[1]
			if c.Header != header || tbl.Rows[0][c.Key] != value {
				t.Fatalf("encoding %d: round trip lost data: header %q -> %q, value %q -> %q", i, header, c.Header, value, tbl.Rows[0][c.Key])
			}
			if i == 0 {
				key = c.Key
			} else if c.Key != key {
				t.Fatalf("encoding %d: key %q, want %q as in UTF-16 LE", i, c.Key, key)
			}
		}
	})
}

// FuzzExtractCSV fuzzes the report ZIP reader, which opens archives fetched
// from a bucket gplay does not control. Two properties:
//
//   - On arbitrary bytes ExtractCSV never panics, never returns members
//     alongside an error, and whatever it returns is a .csv member within the
//     per-member and total budgets.
//   - A well-formed archive holding one CSV member gives that member back
//     byte for byte.
func FuzzExtractCSV(f *testing.F) {
	f.Add([]byte{}, "earnings_202609.csv", "Description,Buyer Country\nGPA.1,FR\n")
	f.Add([]byte("PK\x05\x06"+strings.Repeat("\x00", 18)), "salesreport_202609.csv", "")
	f.Add([]byte("PK\x03\x04"), "a.CSV", "\xff\xfeO\x00r\x00")
	f.Add([]byte("not a zip"), "dir/", "x")

	f.Fuzz(func(t *testing.T, raw []byte, name, body string) {
		ms, err := ExtractCSV(raw)
		if err != nil && ms != nil {
			t.Fatalf("ExtractCSV returned members alongside error %v", err)
		}
		var total uint64
		for _, m := range ms {
			if !strings.EqualFold(path.Ext(m.Name), ".csv") {
				t.Fatalf("returned a non-.csv member %q", m.Name)
			}
			if uint64(len(m.Data)) > MaxZipMemberBytes {
				t.Fatalf("member %q is %d bytes, over the per-member cap", m.Name, len(m.Data))
			}
			total += uint64(len(m.Data))
		}
		if total > MaxZipTotalBytes {
			t.Fatalf("returned %d bytes, over the total cap", total)
		}

		if !strings.EqualFold(path.Ext(name), ".csv") || strings.HasSuffix(name, "/") {
			return
		}
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		fw, err := w.Create(name)
		if err != nil {
			return // a name archive/zip will not write is not a reader defect
		}
		_, _ = fw.Write([]byte(body))
		if err := w.Close(); err != nil {
			t.Fatalf("build fixture: %v", err)
		}
		ms, err = ExtractCSV(buf.Bytes())
		if err != nil {
			t.Fatalf("ExtractCSV rejected a well-formed archive (%q): %v", name, err)
		}
		if len(ms) != 1 || ms[0].Name != name || string(ms[0].Data) != body {
			t.Fatalf("round trip lost data: %+v, want %q holding %q", ms, name, body)
		}
	})
}
