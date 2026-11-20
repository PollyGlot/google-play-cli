package reports

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	MaxReportBytes = 512 << 20
	MaxReportCells = 24 << 20
	rowCells       = 2
)

type Limits struct {
	Bytes int64
	Cells int64
}

var DefaultLimits = Limits{Bytes: MaxReportBytes, Cells: MaxReportCells}

type budget struct {
	lim          Limits
	bytes, cells int64
}

func newBudget(lim Limits) *budget { return &budget{lim: lim} }

func (b *budget) read(n int) error {
	b.bytes += int64(n)
	if b.bytes > b.lim.Bytes {
		return &RefusedError{Reason: "the report passes the " + b.bytesBound() + ": " + narrow}
	}
	return nil
}

func (b *budget) bytesLeft() int64 { return max(b.lim.Bytes-b.bytes, 0) }

func (b *budget) bytesBound() string {
	return fmt.Sprintf("%d-byte budget of one command (decoded bytes, every export together)", b.lim.Bytes)
}

func (b *budget) hold(cells int) error {
	b.cells += int64(cells + rowCells)
	if b.cells > b.lim.Cells {
		return &RefusedError{Reason: fmt.Sprintf("the report passes the %d-cell budget of one command (cells held, every export together; a row counts as its cells plus %d): %s",
			b.lim.Cells, rowCells, narrow)}
	}
	return nil
}

const narrow = "narrow --since/--until to read fewer months at once"

type budgetReader struct {
	r io.Reader
	b *budget
}

func (br *budgetReader) Read(p []byte) (int, error) {
	n, err := br.r.Read(p)
	if n > 0 {
		if e := br.b.read(n); e != nil {
			return 0, e
		}
	}
	return n, err
}

type Reader struct {
	pkg        string
	b          *budget
	zip        zipLimits
	read, kept int
}

func NewReader(pkg string) *Reader { return NewReaderWithLimits(pkg, DefaultLimits) }

func NewReaderWithLimits(pkg string, lim Limits) *Reader {
	return &Reader{pkg: pkg, b: newBudget(lim), zip: defaultZipLimits}
}

func (r *Reader) Read(object string, raw []byte) (Table, error) {
	if !strings.EqualFold(path.Ext(object), ".zip") {
		t, n, err := decodeStream(bytes.NewReader(raw), r.b, r.pkg)
		if err != nil {
			return Table{}, withObject(err, object)
		}
		r.count(n, t)
		return t, nil
	}
	var tables []Table
	n := 0
	err := readZip(raw, r.zip, r.b, func(name string, src io.Reader) error {
		t, read, err := decodeStream(src, r.b, r.pkg)
		if err != nil {
			var refused *RefusedError
			if errors.As(err, &refused) {
				return err
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		n += read
		tables = append(tables, t)
		return nil
	})
	if err != nil {
		return Table{}, err
	}
	t := Merge(tables...)
	r.count(n, t)
	return t, nil
}

func (r *Reader) count(read int, t Table) {
	r.read += read
	r.kept += len(t.Rows)
}

func (r *Reader) RowsRead() int { return r.read }

func (r *Reader) RowsKept() int { return r.kept }
