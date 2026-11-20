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

var bom = []byte{0xEF, 0xBB, 0xBF}

//go:embed reference.csv
var referenceCSV []byte

type Report struct {
	Rows     int
	Columns  int
	Header   []string
	Payload  []byte
	Warnings []string
}

type Problem struct {
	Message string
}

func ReferenceHeader() []string {
	r := csv.NewReader(bytes.NewReader(stripBOM(referenceCSV)))
	rec, err := r.Read()
	if err != nil {
		return nil
	}
	return rec
}

func stripBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, bom)
}

func Validate(raw []byte) (*Report, []Problem) {
	canonical := stripBOM(raw)

	if len(bytes.TrimSpace(canonical)) == 0 {
		return nil, []Problem{{Message: "Data Safety CSV is empty: it must contain a header row and at least one data row (only the live POST validates the contents)"}}
	}
	if !utf8.Valid(canonical) {
		return nil, []Problem{{Message: "Data Safety CSV is not valid UTF-8: re-export it as UTF-8 (a leading byte-order mark is tolerated and stripped)"}}
	}

	r := csv.NewReader(bytes.NewReader(canonical))
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
			"header differs from gplay's bundled reference template: this is a hint, not an error (Google's Data Safety CSV template evolves, and only the live POST validates the declaration). Double-check that --file points at a Data Safety export.")
	}
	return rep, nil
}

func csvProblem(err error) string {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		switch {
		case errors.Is(pe.Err, csv.ErrFieldCount):
			return fmt.Sprintf("Data Safety CSV is not rectangular: row %d has a different column count than the header; every row must have the same number of columns", pe.Line)
		case errors.Is(pe.Err, csv.ErrQuote) || errors.Is(pe.Err, csv.ErrBareQuote):
			return fmt.Sprintf("Data Safety CSV has malformed quoting near line %d: every opening quote must be closed and quotes inside a field must be doubled", pe.Line)
		}
	}
	return "Data Safety CSV does not parse: " + err.Error()
}

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
