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

type Column struct {
	Key    string `json:"key"`
	Header string `json:"header"`
}

type Row struct {
	l     *layout
	cells []string
}

type layout struct {
	pos    map[string]int
	header int
}

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

func (r Row) Value(key string) string {
	v, _ := r.Get(key)
	return v
}

type Table struct {
	Columns []Column
	Rows    []Row
}

func Decode(raw []byte) (Table, error) {
	t, _, err := decodeStream(bytes.NewReader(raw), newBudget(DefaultLimits), "")
	return t, err
}

func DecodeText(raw []byte) (string, error) {
	text, _, err := utf8Text(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(text)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeStream(src io.Reader, b *budget, pkg string) (t Table, read int, err error) {
	text, nonSpace, err := utf8Text(&budgetReader{r: src, b: b})
	if err != nil {
		return Table{}, 0, err
	}
	rd := csv.NewReader(text)
	rd.FieldsPerRecord = -1

	header, err := rd.Read()
	if errors.Is(err, io.EOF) {
		return Table{}, 0, nil
	}
	if err != nil {
		return Table{}, 0, csvError(err)
	}

	used := make(map[string]bool, len(header))
	l := &layout{pos: make(map[string]int, len(header)), header: len(header)}
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
			continue
		}
		added := 0
		for i := len(header); i < len(rec); i++ {
			if rec[i] == "" {
				continue
			}
			if i >= len(keyAt) {
				keyAt = append(keyAt, make([]string, i+1-len(keyAt))...)
			}
			if keyAt[i] == "" {
				keyAt[i] = uniqueKey("", i, used)
				l.pos[keyAt[i]] = i
				t.Columns = append(t.Columns, Column{Key: keyAt[i]})
				added++
			}
		}
		if err := b.hold(len(rec) + added); err != nil {
			return Table{}, 0, err
		}
		t.Rows = append(t.Rows, row)
	}
	if !*nonSpace {
		return Table{}, 0, nil
	}
	return t, read, nil
}

func csvError(err error) error {
	var refused *RefusedError
	if errors.As(err, &refused) {
		return refused
	}
	return fmt.Errorf("parse report CSV: %w", err)
}

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
	out := bufio.NewReaderSize(text, 64<<10)
	if bom, err := out.Peek(3); err == nil && string(bom) == "\uFEFF" {
		_, _ = out.Discard(3)
	}
	st := &spaceTracker{r: out}
	return st, &st.nonSpace, nil
}

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

type utf16Reader struct {
	src     *bufio.Reader
	order   binary.ByteOrder
	pending []byte
	scratch [utf8.UTFMax]byte
	next    uint16
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

func (u *utf16Reader) rune() (rune, error) {
	c, err := u.unit()
	if err != nil {
		return 0, err
	}
	if !utf16.IsSurrogate(rune(c)) {
		return rune(c), nil
	}
	if c >= 0xDC00 {
		return utf8.RuneError, nil
	}
	c2, err := u.unit()
	if errors.Is(err, io.EOF) {
		return utf8.RuneError, nil
	}
	if err != nil {
		return 0, err
	}
	if r := utf16.DecodeRune(rune(c), rune(c2)); r != utf8.RuneError {
		return r, nil
	}
	u.next, u.hasNext = c2, true
	return utf8.RuneError, nil
}

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
