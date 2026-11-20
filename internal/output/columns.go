package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

type Column[T any] struct {
	Key    string
	Header string
	Value  func(T) string
}

type ColumnSet[T any] struct {
	all   []Column[T]
	index map[string]Column[T]
}

func NewColumnSet[T any](cols ...Column[T]) ColumnSet[T] {
	index := make(map[string]Column[T], len(cols))
	for _, c := range cols {
		index[c.Key] = c
	}
	return ColumnSet[T]{all: cols, index: index}
}

func (cs ColumnSet[T]) DefaultKeys() []string {
	keys := make([]string, len(cs.all))
	for i, c := range cs.all {
		keys[i] = c.Key
	}
	return keys
}

func (cs ColumnSet[T]) Resolve(spec string) ([]Column[T], error) {
	if strings.TrimSpace(spec) == "" {
		return cs.all, nil
	}
	parts := strings.Split(spec, ",")
	cols := make([]Column[T], 0, len(parts))
	for _, p := range parts {
		k := strings.TrimSpace(p)
		if k == "" {
			continue
		}
		c, ok := cs.index[k]
		if !ok {
			return nil, exit.Usagef("unknown column %q (valid: %s)", k, strings.Join(cs.DefaultKeys(), ", "))
		}
		cols = append(cols, c)
	}
	if len(cols) == 0 {
		return nil, exit.Usagef("no valid columns in --columns")
	}
	return cols, nil
}

func RenderTable[T any](w io.Writer, cols []Column[T], items []T) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(headers(cols), "\t")); err != nil {
		return err
	}
	for _, it := range items {
		if _, err := fmt.Fprintln(tw, strings.Join(row(cols, it), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func RenderMarkdown[T any](w io.Writer, cols []Column[T], items []T) error {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, row(cols, it))
	}
	return MarkdownTable(w, headers(cols), rows)
}

func headers[T any](cols []Column[T]) []string {
	h := make([]string, len(cols))
	for i, c := range cols {
		h[i] = c.Header
	}
	return h
}

func row[T any](cols []Column[T], item T) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = sanitizeCell(c.Value(item))
	}
	return cells
}
