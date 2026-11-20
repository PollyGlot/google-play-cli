package batch

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Record struct {
	ReviewID string
	Reply    string
}

type Line struct {
	Num    int
	Record Record
	Err    error
}

func Parse(r io.Reader) []Line {
	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.Comment = '#'
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

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
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(rec) != 2 {
			lines = append(lines, Line{Num: num, Err: fmt.Errorf("expected 2 tab-separated fields (review-id and reply), got %d", len(rec))})
			continue
		}
		id := strings.TrimSpace(rec[0])
		if id == "" {
			lines = append(lines, Line{Num: num, Err: errors.New("empty review-id")})
			continue
		}
		if rec[1] == "" {
			lines = append(lines, Line{Num: num, Err: errors.New("empty reply (single mode requires --reply, batch mirrors it)")})
			continue
		}
		lines = append(lines, Line{Num: num, Record: Record{ReviewID: id, Reply: rec[1]}})
	}
	return lines
}

func parseErrLine(err error) int {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return pe.Line
	}
	return 0
}
