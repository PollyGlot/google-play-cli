// Package batch parses the TSV stream consumed by `gplay reviews reply
// --batch`: one `<review-id>\t<reply text>` record per line. It is pure —
// no IO beyond the supplied reader, no exit codes — so the quoting and
// comment/blank rules are exhaustively table-tested here while the command
// layer owns the per-line OK/ERR reporting and the aggregate exit code.
//
// The grammar is RFC 4180 with a TAB field separator (not comma — a comma
// collides too often with reply prose, see issue #62 "out of scope"):
//
//   - Blank lines and lines beginning with `#` are skipped.
//   - A reply containing tabs or newlines must be double-quoted; inside a
//     quoted field a literal quote is written `""`.
//   - Every data line must have exactly two fields with a non-empty
//     review-id; anything else is a malformed Line carrying an Err, and
//     parsing continues with the next line (a per-line failure must not
//     abort the batch). The one unrecoverable case is an unterminated
//     quote: it consumes the rest of the stream by definition, so the tail
//     surfaces as a single error Line.
package batch

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Record is one parsed reply instruction: post Reply as the developer
// response to ReviewID.
type Record struct {
	ReviewID string
	Reply    string
}

// Line is the outcome of parsing one logical TSV line. Exactly one of
// Record (when Err == nil) or Err (a malformed line) is meaningful. Num is
// the 1-based source line where the record starts, for diagnostics.
type Line struct {
	Num    int
	Record Record
	Err    error
}

// Parse reads the whole TSV stream and returns one Line per data line, in
// source order. Blank and `#`-comment lines are skipped and produce no
// Line. It never returns Go-level error itself; every problem is reported
// on the offending Line's Err so the caller can report it and keep going.
func Parse(r io.Reader) []Line {
	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.Comment = '#'
	cr.FieldsPerRecord = -1 // we validate the field count ourselves

	var lines []Line
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			lines = append(lines, Line{Num: parseErrLine(err), Err: err})
			continue
		}
		num, _ := cr.FieldPos(0)
		// csv keeps a whitespace-only line as a single non-empty field;
		// treat it as blank rather than a malformed record.
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(rec) != 2 {
			lines = append(lines, Line{Num: num, Err: fmt.Errorf("expected 2 tab-separated fields (review-id and reply), got %d", len(rec))})
			continue
		}
		if strings.TrimSpace(rec[0]) == "" {
			lines = append(lines, Line{Num: num, Err: errors.New("empty review-id")})
			continue
		}
		lines = append(lines, Line{Num: num, Record: Record{ReviewID: rec[0], Reply: rec[1]}})
	}
	return lines
}

// parseErrLine pulls the source line out of a *csv.ParseError, or 0 when
// the error carries no position.
func parseErrLine(err error) int {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return pe.Line
	}
	return 0
}
