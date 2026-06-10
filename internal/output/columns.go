package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Column is one selectable column of a list-style command's table: its
// canonical --columns key, the header shown in the table/markdown views,
// and the extractor turning a row of type T into that column's cell string.
type Column[T any] struct {
	Key    string
	Header string
	Value  func(T) string
}

// ColumnSet is the full, ordered set of columns a list command can render.
// Its declaration order is BOTH the registry (the valid --columns keys) and
// the default render order — so the "DefaultColumns matches the registry"
// invariant every list command used to assert locally is now a structural
// guarantee: there is a single list, and it cannot drift from itself.
//
// Build one with NewColumnSet, Resolve a --columns spec against it, then
// hand the result to RenderTable / RenderMarkdown.
type ColumnSet[T any] struct {
	all   []Column[T]
	index map[string]Column[T]
}

// NewColumnSet builds a ColumnSet from cols in declaration order. That
// order is the default render order (DefaultKeys) and the set of valid keys
// Resolve accepts.
func NewColumnSet[T any](cols ...Column[T]) ColumnSet[T] {
	index := make(map[string]Column[T], len(cols))
	for _, c := range cols {
		index[c.Key] = c
	}
	return ColumnSet[T]{all: cols, index: index}
}

// DefaultKeys returns the canonical column keys in declaration order — the
// set rendered when --columns is unset. A command builds its --columns flag
// help from this so the documented default cannot drift from the code.
func (cs ColumnSet[T]) DefaultKeys() []string {
	keys := make([]string, len(cs.all))
	for i, c := range cs.all {
		keys[i] = c.Key
	}
	return keys
}

// Resolve turns a --columns spec into the ordered, validated columns to
// render. An empty/whitespace spec yields every column in declaration
// order; otherwise the spec is split on commas, each key trimmed and looked
// up, and blank entries skipped. An unknown key — or a spec that names no
// valid column at all — is a CLI misuse (*exit.UsageError, exit 2), with the
// valid set listed in the message.
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

// RenderTable writes a tab-aligned table of cols over items: a header row,
// then one row per item. The tabwriter config (minwidth 0, tabwidth 2,
// padding 2, space padding) matches the byte-for-byte layout every list
// command produced before this helper existed.
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

// RenderMarkdown writes cols over items as a GitHub-Flavored Markdown table
// via MarkdownTable (which escapes any `|` in the cells).
func RenderMarkdown[T any](w io.Writer, cols []Column[T], items []T) error {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		rows = append(rows, row(cols, it))
	}
	return MarkdownTable(w, headers(cols), rows)
}

// headers returns cols' display headers, in order.
func headers[T any](cols []Column[T]) []string {
	h := make([]string, len(cols))
	for i, c := range cols {
		h[i] = c.Header
	}
	return h
}

// row extracts cols' cells for one item, in order. Columns reaching here
// come from Resolve, so every Value is non-nil. Each cell is sanitized
// (sanitizeCell) because column values often carry untrusted API strings
// (review text, listing copy): this is the central table render boundary where
// ANSI escapes and control characters are neutralized for human output. The
// JSON renderer does not pass through here, so it stays byte-faithful (ADR-0003).
func row[T any](cols []Column[T], item T) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = sanitizeCell(c.Value(item))
	}
	return cells
}
