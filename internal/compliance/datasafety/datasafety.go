// Package datasafety owns the OFFLINE structural validation of an app's
// Data Safety CSV — the canonical on-disk artifact for the
// applications.dataSafety declaration (ADR-0014). It is pure and
// network-free: it parses the CSV, checks it is well-formed and rectangular,
// and reports a non-fatal hint when the header diverges from a bundled
// reference template.
//
// It makes NO semantic claim. gplay does not own — and cannot read back —
// Google's Data Safety schema (the API is write-only), so "structurally
// valid" is deliberately weaker than "Google will accept it": only the live
// POST arbitrates the declaration's correctness. This is the file-only half
// of the compliance datasafety surface; `set` reuses Validate before posting.
package datasafety

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// bom is the UTF-8 byte-order mark. A leading BOM is a common artifact of
// CSVs exported from spreadsheet tools; we repair it (strip it) rather than
// reject the file, and the canonical Payload we hand to `set` carries none.
var bom = []byte{0xEF, 0xBB, 0xBF}

// referenceCSV is the bundled reference template — a SNAPSHOT of the Play
// Console Data Safety CSV header at the time of writing. Google evolves this
// template, and gplay can neither read the live declaration nor own the
// schema (ADR-0014), so a divergence from it is reported as a NON-FATAL
// warning (a hint that your CSV may predate or postdate this gplay release),
// never a structural error. Only the live POST validates the declaration.
//
//go:embed reference.csv
var referenceCSV []byte

// Report summarizes a structurally validated Data Safety CSV. It is returned
// only when there are no structural Problems; on a structural failure
// Validate returns (nil, problems).
type Report struct {
	// Rows is the number of data rows, excluding the header row.
	Rows int
	// Columns is the column count (the header width).
	Columns int
	// Header is the parsed header row fields.
	Header []string
	// Payload is the canonical document gplay would POST: the input with a
	// leading BOM stripped. It is exactly what `set` sends as safetyLabels.
	Payload []byte
	// Warnings carries non-fatal hints (e.g. a header that diverges from the
	// bundled reference template). A warning never fails the command.
	Warnings []string
}

// Problem is a structural validation failure. One actionable message each;
// the command maps a non-empty problem list to exit 20.
type Problem struct {
	Message string
}

// ReferenceHeader returns the header fields of the bundled reference
// template. Exposed so callers — and tests — can see exactly what gplay
// compares an input header against. The template is a snapshot and may
// drift from Google's current export, which is why a mismatch is a
// non-fatal warning rather than an error.
func ReferenceHeader() []string {
	r := csv.NewReader(bytes.NewReader(stripBOM(referenceCSV)))
	rec, err := r.Read()
	if err != nil {
		return nil
	}
	return rec
}

// stripBOM returns raw with a single leading UTF-8 BOM removed, if present.
func stripBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, bom)
}

// Validate runs OFFLINE structural checks on a raw Data Safety CSV:
//
//   - non-empty (after BOM repair)
//   - sane UTF-8 (a leading BOM is stripped; otherwise invalid UTF-8 fails)
//   - parses as CSV with balanced quoting and rectangular rows
//   - has a header row
//
// It makes no semantic claim. A header that diverges from the bundled
// reference template is a non-fatal Warning on the Report, never a Problem.
// On any structural failure it returns (nil, problems); on success it
// returns a populated Report and a nil problem slice.
func Validate(raw []byte) (*Report, []Problem) {
	canonical := stripBOM(raw)

	if len(bytes.TrimSpace(canonical)) == 0 {
		return nil, []Problem{{Message: "Data Safety CSV is empty — it must contain a header row and at least one data row (only the live POST validates the contents)"}}
	}
	if !utf8.Valid(canonical) {
		return nil, []Problem{{Message: "Data Safety CSV is not valid UTF-8 — re-export it as UTF-8 (a leading byte-order mark is tolerated and stripped)"}}
	}

	r := csv.NewReader(bytes.NewReader(canonical))
	// FieldsPerRecord defaults to 0: the first record sets the expected
	// column count and a later row with a different count is an error — that
	// is exactly the rectangular-rows guarantee we want for free.
	records, err := r.ReadAll()
	if err != nil {
		return nil, []Problem{{Message: csvProblem(err)}}
	}
	if len(records) == 0 {
		return nil, []Problem{{Message: "Data Safety CSV has no header row"}}
	}

	header := records[0]
	rep := &Report{
		Rows:    len(records) - 1,
		Columns: len(header),
		Header:  header,
		Payload: canonical,
	}
	if !headerMatchesReference(header) {
		rep.Warnings = append(rep.Warnings,
			"header differs from gplay's bundled reference template — this is a hint, not an error (Google's Data Safety CSV template evolves, and only the live POST validates the declaration). Double-check that --file points at a Data Safety export.")
	}
	return rep, nil
}

// csvProblem turns an encoding/csv parse error into one actionable message,
// distinguishing the rectangular-rows failure (ErrFieldCount) from the
// quoting failures so the operator knows which lever to pull.
func csvProblem(err error) string {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		switch {
		case errors.Is(pe.Err, csv.ErrFieldCount):
			return fmt.Sprintf("Data Safety CSV is not rectangular — row %d has a different column count than the header; every row must have the same number of columns", pe.Line)
		case errors.Is(pe.Err, csv.ErrQuote) || errors.Is(pe.Err, csv.ErrBareQuote):
			return fmt.Sprintf("Data Safety CSV has malformed quoting near line %d — every opening quote must be closed and quotes inside a field must be doubled", pe.Line)
		}
	}
	return "Data Safety CSV does not parse: " + err.Error()
}

// headerMatchesReference reports whether header equals the bundled reference
// header, comparing field-by-field case-insensitively after trimming
// surrounding whitespace (so trivial casing/whitespace differences do not
// raise a spurious hint).
func headerMatchesReference(header []string) bool {
	ref := ReferenceHeader()
	if len(header) != len(ref) {
		return false
	}
	for i := range ref {
		if !strings.EqualFold(strings.TrimSpace(header[i]), strings.TrimSpace(ref[i])) {
			return false
		}
	}
	return true
}
