package reports

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Column is one column of a decoded report: the JSON key gplay derives from
// the CSV header, and that header verbatim. Keys are what --columns and the
// `rows` objects use; the header is what Google documents.
type Column struct {
	Key    string `json:"key"`
	Header string `json:"header"`
}

// Row is one row of a decoded report: its cells, verbatim, as the CSV record
// carried them, read by column key. A key is absent when the row has no cell
// for that column (a short row, an empty cell past the header, or a month
// that predates a column in a merged range); an empty cell within the header
// is present as "".
//
// A row is its record's cells plus a pointer to the column layout its export
// shares, not a map: a map per row cost about 170 times the CSV it came from,
// and a report of millions of rows held gigabytes (ADR-0050 §10).
type Row struct {
	l     *layout
	cells []string
}

// layout maps the column keys of one decoded export to cell positions. Every
// row of the export points to it.
type layout struct {
	pos map[string]int
	// header is the header width: an empty cell at or past it is no cell
	// (a trailing separator), so it reads as absent.
	header int
}

// Get returns the row's cell for key, and whether the row has one.
func (r Row) Get(key string) (string, bool) {
	if r.l == nil {
		return "", false
	}
	i, ok := r.l.pos[key]
	if !ok || i >= len(r.cells) {
		return "", false
	}
	v := r.cells[i]
	if v == "" && i >= r.l.header {
		return "", false
	}
	return v, true
}

// Value returns the row's cell for key, "" when the row has none.
func (r Row) Value(key string) string {
	v, _ := r.Get(key)
	return v
}

// Table is a decoded report: its columns in header order and its rows in file
// order. Nothing in it is typed: every value stays the string the CSV carried,
// so postcodes, product ids and version codes keep their leading zeros.
type Table struct {
	Columns []Column
	Rows    []Row
}

// Decode turns the bytes of one monthly export into a Table, under the
// default budget of one command (DefaultLimits). The encoding is detected,
// never assumed: Google documents the statistics files as UTF-16 but not
// every family (the CSVs inside the finance ZIPs are unconfirmed), so a BOM
// picks UTF-16 LE, UTF-16 BE or UTF-8, a BOM-less file starting with a NUL
// byte pattern is read as UTF-16, and anything else as UTF-8. UTF-16 with an
// odd number of bytes was cut mid character and is refused. The CSV is
// RFC 4180 (quoted fields may hold commas, quotes and newlines). The header
// row drives the columns, so a column Google adds, renames or drops flows
// through with no gplay release.
//
// An empty file or a file with only a header is an empty report, not an error.
// A malformed CSV fails and returns no rows.
func Decode(raw []byte) (Table, error) {
	t, _, err := decodeStream(bytes.NewReader(raw), newBudget(DefaultLimits), "")
	return t, err
}

// decodeStream decodes one CSV export from src as it is read, never holding
// the file whole: the bytes read are charged to b (decoded bytes) and so are
// the rows kept (cells held). With pkg set, only the rows whose package
// column (packageKeys, resolved on this export's own header) holds pkg are
// kept, so the rows --package drops never take memory. read is the number of
// data rows the export held, kept or not.
func decodeStream(src io.Reader, b *budget, pkg string) (t Table, read int, err error) {
	text, nonSpace, err := utf8Text(&budgetReader{r: src, b: b})
	if err != nil {
		return Table{}, 0, err
	}
	rd := csv.NewReader(text)
	rd.FieldsPerRecord = -1 // a ragged row is kept, not rejected

	header, err := rd.Read()
	if errors.Is(err, io.EOF) {
		return Table{}, 0, nil
	}
	if err != nil {
		return Table{}, 0, csvError(err)
	}

	used := make(map[string]bool, len(header))
	l := &layout{pos: make(map[string]int, len(header)), header: len(header)}
	// keyAt[i] is the key of the cell at position i; "" marks a position past
	// the header that no row has filled yet.
	keyAt := make([]string, 0, len(header))
	for i, h := range header {
		k := uniqueKey(keyFor(h), i, used)
		keyAt = append(keyAt, k)
		l.pos[k] = i
		t.Columns = append(t.Columns, Column{Key: k, Header: strings.TrimSpace(h)})
	}
	if err := b.hold(len(header)); err != nil {
		return Table{}, 0, err
	}
	pkgKey := ""
	if pkg != "" {
		if pkgKey, err = packageKey(t.Columns); err != nil {
			return Table{}, 0, err
		}
	}

	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Table{}, 0, csvError(err)
		}
		read++
		row := Row{l: l, cells: rec}
		if pkgKey != "" && strings.TrimSpace(row.Value(pkgKey)) != pkg {
			continue // dropped before it can grow the table
		}
		added := 0
		for i := len(header); i < len(rec); i++ {
			if rec[i] == "" {
				continue // a trailing separator past the header, not data
			}
			if i >= len(keyAt) {
				keyAt = append(keyAt, make([]string, i+1-len(keyAt))...)
			}
			if keyAt[i] == "" {
				// A cell beyond the header: kept under a positional key so a
				// malformed row loses nothing.
				keyAt[i] = uniqueKey("", i, used)
				l.pos[keyAt[i]] = i
				t.Columns = append(t.Columns, Column{Key: keyAt[i]})
				added++
			}
		}
		// A positional column costs a cell of bookkeeping, like a header
		// cell: charged so one very wide row cannot bypass the budget.
		if err := b.hold(len(rec) + added); err != nil {
			return Table{}, 0, err
		}
		t.Rows = append(t.Rows, row)
	}
	if !*nonSpace {
		// Only blanks: an empty report, like an empty file.
		return Table{}, 0, nil
	}
	return t, read, nil
}

// csvError keeps a refusal from the reader underneath (a crossed budget, an
// odd UTF-16 file, a member that will not inflate) as the refusal it is, and
// labels anything else a CSV parse failure.
func csvError(err error) error {
	var refused *RefusedError
	if errors.As(err, &refused) {
		return refused
	}
	return fmt.Errorf("parse report CSV: %w", err)
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

// utf8Text returns src as UTF-8 text, transcoded as it is read, its BOM
// removed. A BOM picks the encoding. Without one, a first code unit holding
// one NUL byte means UTF-16 (a UTF-8 CSV never starts with a NUL): the side
// of the NUL gives the byte order. Everything else is UTF-8, passed through
// untouched. nonSpace turns true once a byte other than ASCII white space has
// gone through: a file of blanks only is an empty report.
func utf8Text(src io.Reader) (io.Reader, *bool, error) {
	br := bufio.NewReaderSize(src, 64<<10)
	head, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, nil, err
	}
	var text io.Reader = br
	switch {
	case len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE:
		_, _ = br.Discard(2)
		text = &utf16Reader{src: br, order: binary.LittleEndian}
	case len(head) >= 2 && head[0] == 0xFE && head[1] == 0xFF:
		_, _ = br.Discard(2)
		text = &utf16Reader{src: br, order: binary.BigEndian}
	case len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF:
		_, _ = br.Discard(3)
	case len(head) >= 2 && head[0] != 0 && head[1] == 0:
		text = &utf16Reader{src: br, order: binary.LittleEndian}
	case len(head) >= 2 && head[0] == 0 && head[1] != 0:
		text = &utf16Reader{src: br, order: binary.BigEndian}
	}
	// One U+FEFF after the BOM (or alone, in a BOM-less file) is not text
	// either.
	out := bufio.NewReaderSize(text, 64<<10)
	if bom, err := out.Peek(3); err == nil && string(bom) == "\uFEFF" {
		_, _ = out.Discard(3)
	}
	st := &spaceTracker{r: out}
	return st, &st.nonSpace, nil
}

// spaceTracker passes text through and records whether any of it is not
// ASCII white space.
type spaceTracker struct {
	r        io.Reader
	nonSpace bool
}

func (s *spaceTracker) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if !s.nonSpace {
		for _, c := range p[:n] {
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\v' && c != '\f' {
				s.nonSpace = true
				break
			}
		}
	}
	return n, err
}

// utf16Reader transcodes UTF-16 to UTF-8 as it is read. A surrogate pair
// gives its rune and a lone surrogate U+FFFD, as unicode/utf16.Decode does. A
// trailing odd byte means the file was cut mid character: it is refused,
// never dropped.
type utf16Reader struct {
	src     *bufio.Reader
	order   binary.ByteOrder
	pending []byte // UTF-8 bytes of a rune p had no room for
	scratch [utf8.UTFMax]byte
	next    uint16 // a code unit read ahead after a lone high surrogate
	hasNext bool
	err     error
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(u.pending) > 0 {
			c := copy(p[n:], u.pending)
			u.pending = u.pending[c:]
			n += c
			continue
		}
		if u.err != nil {
			break
		}
		r, err := u.rune()
		if err != nil {
			u.err = err
			break
		}
		if r < utf8.RuneSelf {
			p[n] = byte(r)
			n++
			continue
		}
		if utf8.RuneLen(r) <= len(p)-n {
			n += utf8.EncodeRune(p[n:], r)
			continue
		}
		u.pending = u.scratch[:utf8.EncodeRune(u.scratch[:], r)]
	}
	if n > 0 {
		return n, nil
	}
	return 0, u.err
}

// rune decodes the next rune.
func (u *utf16Reader) rune() (rune, error) {
	c, err := u.unit()
	if err != nil {
		return 0, err
	}
	if !utf16.IsSurrogate(rune(c)) {
		return rune(c), nil
	}
	if c >= 0xDC00 {
		return utf8.RuneError, nil // a low surrogate with no high one
	}
	c2, err := u.unit()
	if errors.Is(err, io.EOF) {
		return utf8.RuneError, nil // a high surrogate at the end of the file
	}
	if err != nil {
		return 0, err
	}
	if r := utf16.DecodeRune(rune(c), rune(c2)); r != utf8.RuneError {
		return r, nil
	}
	u.next, u.hasNext = c2, true // not its pair: c2 is the next unit
	return utf8.RuneError, nil
}

// unit reads one code unit.
func (u *utf16Reader) unit() (uint16, error) {
	if u.hasNext {
		u.hasNext = false
		return u.next, nil
	}
	var b [2]byte
	n, err := io.ReadFull(u.src, b[:])
	switch {
	case n == 2:
		return u.order.Uint16(b[:]), nil
	case n == 1:
		return 0, &RefusedError{Reason: "UTF-16 export with an odd number of bytes: the file is cut mid character"}
	case errors.Is(err, io.EOF):
		return 0, io.EOF
	}
	return 0, err
}

// Merge joins the tables of several monthly exports, in the order given, into
// one result set. Its columns are the union of theirs in first-seen order (the
// first header seen for a key is kept), and rows keep the cells they had: a
// month that predates a column Google added has no cell for it, so a range
// across a schema change never fails nor misaligns. Rows are not deduplicated
// nor re-sorted: each export covers its own month, so file order is date order.
func Merge(tables ...Table) Table {
	if len(tables) == 1 {
		return tables[0]
	}
	var out Table
	n := 0
	for _, t := range tables {
		n += len(t.Rows)
	}
	if n > 0 {
		out.Rows = make([]Row, 0, n)
	}
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
