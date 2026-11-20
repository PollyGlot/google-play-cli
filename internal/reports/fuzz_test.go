package reports

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"io"
	"math/rand/v2"
	"path"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf16"
	"unicode/utf8"
)

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
			for k := range r.l.pos {
				if _, ok := r.Get(k); ok && !declared[k] {
					t.Fatalf("Decode(%q) row key %q is not a column", raw, k)
				}
			}
		}

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
			if c.Header != header || tbl.Rows[0].Value(c.Key) != value {
				t.Fatalf("encoding %d: round trip lost data: header %q -> %q, value %q -> %q", i, header, c.Header, value, tbl.Rows[0].Value(c.Key))
			}
			if i == 0 {
				key = c.Key
			} else if c.Key != key {
				t.Fatalf("encoding %d: key %q, want %q as in UTF-16 LE", i, c.Key, key)
			}
		}
	})
}

func FuzzExtractCSV(f *testing.F) {
	f.Add([]byte{}, "earnings_202609.csv", "Description,Buyer Country\nGPA.1,FR\n")
	f.Add([]byte("PK\x05\x06"+strings.Repeat("\x00", 18)), "salesreport_202609.csv", "")
	f.Add([]byte("PK\x03\x04"), "a.CSV", "\xff\xfeO\x00r\x00")
	f.Add([]byte("not a zip"), "dir/", "x")

	f.Fuzz(func(t *testing.T, raw []byte, name, body string) {
		tbl, err := NewReader("").Read("earnings/earnings_202609.zip", raw)
		if err != nil && (tbl.Rows != nil || tbl.Columns != nil) {
			t.Fatalf("Read returned a table alongside error %v", err)
		}

		if !strings.EqualFold(path.Ext(name), ".csv") || strings.HasSuffix(name, "/") {
			return
		}
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		fw, err := w.Create(name)
		if err != nil {
			return
		}
		_, _ = fw.Write([]byte(body))
		if err := w.Close(); err != nil {
			t.Fatalf("build fixture: %v", err)
		}
		got, err := NewReader("").Read("earnings/earnings_202609.zip", buf.Bytes())
		want, wantErr := Decode([]byte(body))
		if (err != nil) != (wantErr != nil) {
			t.Fatalf("archive read err %v, member decoded alone err %v", err, wantErr)
		}
		if err != nil {
			return
		}
		if !sameTable(got, want) {
			t.Fatalf("round trip lost data: %+v, want %+v", got, want)
		}
	})
}

func TestUTF16Reader_matchesUnicodeUTF16(t *testing.T) {
	cases := [][]byte{
		{'a', 0, 0x3D, 0xD8, 0x89, 0xDF},
		{0x3D, 0xD8, 'a', 0},
		{0x89, 0xDF, 'a', 0},
		{'a', 0, 0x3D, 0xD8},
		{0x3D, 0xD8, 0x3D, 0xD8, 0x89, 0xDF},
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		b := make([]byte, 2*rng.IntN(12))
		for i := range b {
			if i%2 == 1 && rng.IntN(2) == 0 {
				b[i] = byte(0xD8 + rng.IntN(8))
			} else {
				b[i] = byte(rng.IntN(256))
			}
		}
		cases = append(cases, b)
	}
	for _, raw := range cases {
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(raw[2*i:])
		}
		got, err := io.ReadAll(iotest.OneByteReader(&utf16Reader{src: bufio.NewReader(bytes.NewReader(raw)), order: binary.LittleEndian}))
		if err != nil {
			t.Fatalf("read %x: %v", raw, err)
		}
		if want := string(utf16.Decode(units)); string(got) != want {
			t.Fatalf("%x transcoded %q, want %q", raw, got, want)
		}
	}
}

func sameTable(a, b Table) bool {
	if len(a.Columns) != len(b.Columns) || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	for i := range a.Rows {
		ra, rb := cellsOf(a.Rows[i], a.Columns), cellsOf(b.Rows[i], b.Columns)
		if len(ra) != len(rb) {
			return false
		}
		for k, v := range ra {
			if w, ok := rb[k]; !ok || w != v {
				return false
			}
		}
	}
	return true
}
