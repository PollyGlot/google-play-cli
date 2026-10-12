package reports

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// The budget of one command (ADR-0050 §10). Every export a command reads (a
// month, each file of a month, each member of an archive) draws on the same
// budget, so a --since/--until range is bounded as a whole, not per export.
// Two quantities are bounded, because neither alone bounds the memory a
// report takes: the decoded bytes (every byte read from a CSV, compressed
// archives excepted) and the cells held (what the rows kept cost, whatever
// their bytes: a file of one-byte rows is small on disk and large in memory).
// Measured on the compact row (Row), a row costs about 32 bytes plus 16 a
// cell plus its text; counting a row as its cells plus rowCells makes a cell
// weigh about 28 bytes, text included, on one-cell rows as on 24-column
// earnings rows, so at the cell bound the rows held weigh about 700 MB
// whatever their shape (ADR-0050 §10 has the measures).
const (
	// MaxReportBytes caps the bytes one command decodes, every export
	// together.
	MaxReportBytes = 512 << 20
	// MaxReportCells caps the cells one command holds, every export
	// together; a row counts as its cells plus rowCells.
	MaxReportCells = 24 << 20
	// rowCells is what a row's own bookkeeping weighs, in cells.
	rowCells = 2
)

// Limits are the budget of one command: MaxReportBytes and MaxReportCells in
// production (DefaultLimits); the tests pass small ones.
type Limits struct {
	Bytes int64 // decoded bytes, every export together
	Cells int64 // cells held, every export together; a row counts as its cells plus two
}

// DefaultLimits is the production budget.
var DefaultLimits = Limits{Bytes: MaxReportBytes, Cells: MaxReportCells}

// budget is what is left of a command's Limits.
type budget struct {
	lim          Limits
	bytes, cells int64
}

func newBudget(lim Limits) *budget { return &budget{lim: lim} }

// read charges n decoded bytes.
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

// hold charges a row of cells kept.
func (b *budget) hold(cells int) error {
	b.cells += int64(cells + rowCells)
	if b.cells > b.lim.Cells {
		return &RefusedError{Reason: fmt.Sprintf("the report passes the %d-cell budget of one command (cells held, every export together; a row counts as its cells plus %d): %s",
			b.lim.Cells, rowCells, narrow)}
	}
	return nil
}

// narrow is the way out of a crossed budget.
const narrow = "narrow --since/--until to read fewer months at once"

// budgetReader charges the bytes read from an export to the budget, and
// refuses past it.
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

// Reader reads the exports of one command: every export it reads draws on
// one budget (Limits), and, for an account-wide family, keeps only the rows
// of one package. A command builds one Reader and reads each export with it.
type Reader struct {
	pkg        string
	b          *budget
	zip        zipLimits
	read, kept int
}

// NewReader returns a Reader under the production budget. pkg, when set,
// keeps only the rows whose package column (Package Name, Package ID, or
// Product ID as the finance reports call it, resolved on each export's own
// header) holds it; "" keeps every row.
func NewReader(pkg string) *Reader { return NewReaderWithLimits(pkg, DefaultLimits) }

// NewReaderWithLimits is NewReader under a chosen budget.
func NewReaderWithLimits(pkg string, lim Limits) *Reader {
	return &Reader{pkg: pkg, b: newBudget(lim), zip: defaultZipLimits}
}

// Read decodes one export, named object, into a Table. The object's
// extension picks the decoding: a .zip export's .csv members are streamed
// out under ADR-0050's bounds, each decoded with its own encoding detected
// (by BOM), and merged in archive order; any other export (.csv, which the
// account-wide name shape also accepts) is decoded as is. Nothing is held
// whole but the export's own bytes: each CSV is decoded as it is read. A
// crossed bound is a *RefusedError (exit 20) naming the export.
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

// RowsRead is the number of data rows the exports read held, before the
// package filter.
func (r *Reader) RowsRead() int { return r.read }

// RowsKept is the number of rows kept.
func (r *Reader) RowsKept() int { return r.kept }
