package reports

import (
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Column is one column of a decoded report: the JSON key gplay derives from
// the CSV header, and that header verbatim. Keys are what --columns and the
// `rows` objects use; the header is what Google documents.
type Column struct {
	Key    string `json:"key"`
	Header string `json:"header"`
}

// Row maps a column key to its cell, verbatim. A key is absent when the row
// has no cell for that column (a short row, or a month that predates a column
// in a merged range); an empty cell is present as "".
type Row map[string]string

// Table is a decoded report: its columns in header order and its rows in file
// order. Nothing in it is typed: every value stays the string the CSV carried,
// so postcodes, product ids and version codes keep their leading zeros.
type Table struct {
	Columns []Column
	Rows    []Row
}

// Decode turns the bytes of one monthly export into a Table. The encoding is
// detected, never assumed: Google documents the statistics files as UTF-16
// but not every family (the CSVs inside the finance ZIPs are unconfirmed), so
// a BOM picks UTF-16 LE, UTF-16 BE or UTF-8, a BOM-less file starting with a
// NUL byte pattern is read as UTF-16, and anything else as UTF-8. The CSV is
// RFC 4180 (quoted fields may hold commas, quotes and newlines). The header
// row drives the columns, so a column Google adds, renames or drops flows
// through with no gplay release.
//
// An empty file or a file with only a header is an empty report, not an error.
// A malformed CSV fails and returns no rows.
func Decode(raw []byte) (Table, error) {
	text, err := decodeText(raw)
	if err != nil {
		return Table{}, err
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	if strings.TrimSpace(text) == "" {
		return Table{}, nil
	}
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1 // a ragged row is kept, not rejected

	header, err := rd.Read()
	if errors.Is(err, io.EOF) {
		return Table{}, nil
	}
	if err != nil {
		return Table{}, fmt.Errorf("parse report CSV: %w", err)
	}

	used := make(map[string]bool, len(header))
	t := Table{Columns: make([]Column, 0, len(header))}
	// keyAt[i] is the key of the cell at position i; "" marks a position past
	// the header that no row has filled yet.
	keyAt := make([]string, 0, len(header))
	for i, h := range header {
		k := uniqueKey(keyFor(h), i, used)
		keyAt = append(keyAt, k)
		t.Columns = append(t.Columns, Column{Key: k, Header: strings.TrimSpace(h)})
	}

	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Table{}, fmt.Errorf("parse report CSV: %w", err)
		}
		row := make(Row, len(rec))
		for i, cell := range rec {
			if i >= len(header) && cell == "" {
				continue // a trailing separator past the header, not data
			}
			if i >= len(keyAt) {
				keyAt = append(keyAt, make([]string, i+1-len(keyAt))...)
			}
			if keyAt[i] == "" {
				// A cell beyond the header: kept under a positional key so a
				// malformed row loses nothing.
				keyAt[i] = uniqueKey("", i, used)
				t.Columns = append(t.Columns, Column{Key: keyAt[i]})
			}
			row[keyAt[i]] = cell
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

// keyFor derives the JSON key of a CSV header, the rule ADR-0050 documents so
// a reader can predict a key from the column name Google publishes: the words
// are the runs of letters and digits; the first is lowercased, each next one
// gets an upper-case first letter and the rest lowercased. "Daily Device
// Installs" gives dailyDeviceInstalls, "Daily ANRs" dailyAnrs, "Store listing
// acquisitions (7-day)" storeListingAcquisitions7Day. A header with no word
// gives "" (uniqueKey turns it positional).
func keyFor(header string) string {
	words := strings.FieldsFunc(header, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for i, w := range words {
		rs := []rune(strings.ToLower(w))
		if i > 0 {
			rs[0] = unicode.ToUpper(rs[0])
		}
		b.WriteString(string(rs))
	}
	return b.String()
}

// uniqueKey makes key unique among used and records it: a key with no word
// becomes column<N> (N the 1-based position), and a repeat gets the first free
// numeric suffix from 2, so "Installs, Installs" gives installs, installs2.
func uniqueKey(key string, pos int, used map[string]bool) string {
	if key == "" {
		key = "column" + strconv.Itoa(pos+1)
	}
	k := key
	for n := 2; used[k]; n++ {
		k = key + strconv.Itoa(n)
	}
	used[k] = true
	return k
}

// decodeText transcodes a report to UTF-8 by its BOM. Without a BOM, a first
// code unit holding one NUL byte means UTF-16 (a UTF-8 CSV never starts with a
// NUL): the side of the NUL gives the byte order. Everything else is UTF-8,
// passed through untouched. UTF-16 with an odd number of bytes was cut mid
// code unit: it is refused, never decoded with its last character dropped.
func decodeText(b []byte) (string, error) {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b[2:], binary.LittleEndian)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b[2:], binary.BigEndian)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:]), nil
	case len(b) >= 2 && b[0] != 0 && b[1] == 0:
		return decodeUTF16(b, binary.LittleEndian)
	case len(b) >= 2 && b[0] == 0 && b[1] != 0:
		return decodeUTF16(b, binary.BigEndian)
	}
	return string(b), nil
}

func decodeUTF16(b []byte, order binary.ByteOrder) (string, error) {
	if len(b)%2 != 0 {
		return "", oddUTF16()
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, order.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(u16)), nil
}

// oddUTF16 is the refusal of a UTF-16 export cut mid code unit.
func oddUTF16() error {
	return &RefusedError{Reason: "UTF-16 export with an odd number of bytes: the file is cut mid character"}
}

// Merge joins the tables of several monthly exports, in the order given, into
// one result set. Its columns are the union of theirs in first-seen order (the
// first header seen for a key is kept), and rows keep the cells they had: a
// month that predates a column Google added has no cell for it, so a range
// across a schema change never fails nor misaligns. Rows are not deduplicated
// nor re-sorted: each export covers its own month, so file order is date order.
func Merge(tables ...Table) Table {
	var out Table
	seen := map[string]bool{}
	for _, t := range tables {
		for _, c := range t.Columns {
			if !seen[c.Key] {
				seen[c.Key] = true
				out.Columns = append(out.Columns, c)
			}
		}
		out.Rows = append(out.Rows, t.Rows...)
	}
	return out
}
