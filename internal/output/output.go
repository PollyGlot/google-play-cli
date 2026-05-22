// Package output owns Format selection and rendering dispatch for gplay
// commands. Every command supplies its three Renderers (table / json /
// markdown) and calls Render; this package resolves the requested Format
// against TTY state and the CI env var, then picks the matching Renderer.
//
// See docs/adr/0005-tty-aware-output.md for the design rationale and the
// rejected alternatives.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Format is the user-facing output shape requested via --output. The empty
// string (FormatAuto) is the flag default; the dispatcher resolves it
// against the runtime context.
type Format string

const (
	FormatAuto     Format = ""
	FormatTable    Format = "table"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// IsTerminalFn reports whether w is connected to a terminal. The default
// implementation type-asserts w to *os.File and calls term.IsTerminal on
// its fd; non-file writers are treated as non-TTY so JSON wins under any
// test or pipe.
type IsTerminalFn func(w io.Writer) bool

var isTTY IsTerminalFn = defaultIsTTY

func defaultIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// IsTerminalFunc returns the current TTY-detection hook. Tests use it
// alongside SetIsTerminalFunc to swap and restore the hook.
func IsTerminalFunc() IsTerminalFn { return isTTY }

// SetIsTerminalFunc installs a replacement TTY detector. Production code
// never calls this; it exists so tests can force TTY/non-TTY without a
// pty.
func SetIsTerminalFunc(fn IsTerminalFn) { isTTY = fn }

// Renderers carries one rendering function per Format. A nil field means
// the command does not support that Format; Render returns a uniform
// "unsupported" error when the resolved Format hits a nil field.
type Renderers struct {
	Table    func(io.Writer) error
	JSON     func(io.Writer) error
	Markdown func(io.Writer) error
}

// Render resolves the requested Format for w, picks the matching field
// from r, and runs it. A nil field surfaces the same uniform
// "unsupported --output" error as an unknown Format — so a command that
// ships without (say) a Markdown renderer behaves identically to a typo.
func Render(w io.Writer, requested Format, r Renderers) error {
	f, err := Resolve(requested, w)
	if err != nil {
		return err
	}
	var fn func(io.Writer) error
	switch f {
	case FormatTable:
		fn = r.Table
	case FormatJSON:
		fn = r.JSON
	case FormatMarkdown:
		fn = r.Markdown
	}
	if fn == nil {
		return unsupportedFormat(f)
	}
	return fn(w)
}

// WriteJSON streams v to w as indented JSON using the gplay-wide
// "2-space indent, trailing newline" shape. Every command's JSON
// renderer should call this — keeping the encoder configuration in one
// place stops the SetIndent setting from drifting between commands.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// RegisterFlag binds the standard --output flag to dest on cmd. Every
// command should use this helper rather than calling StringVar directly,
// so the flag name, default (empty = auto), and help text stay
// consistent across commands and can evolve in one place.
func RegisterFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "output", "",
		"output format: table, json, or markdown (default: auto — table on TTY, json in pipes/CI)")
}

// MarkdownTable writes a standard GitHub-Flavored Markdown table: one
// header row, the `| --- | --- |` separator, then one row per data line.
// Cell contents containing `|` are escaped to `\|` so the resulting
// table is valid no matter what a caller passes in. A row whose width
// does not match the header width is a programmer error and returns an
// error mentioning the offending row index.
func MarkdownTable(w io.Writer, headers []string, rows [][]string) error {
	if _, err := fmt.Fprintf(w, "| %s |\n", joinCells(headers)); err != nil {
		return err
	}
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	if _, err := fmt.Fprintf(w, "| %s |\n", strings.Join(sep, " | ")); err != nil {
		return err
	}
	for i, row := range rows {
		if len(row) != len(headers) {
			return fmt.Errorf("markdown table row %d has %d cells; want %d", i, len(row), len(headers))
		}
		if _, err := fmt.Fprintf(w, "| %s |\n", joinCells(row)); err != nil {
			return err
		}
	}
	return nil
}

// joinCells escapes any literal `|` in each cell to `\|` (the GitHub
// Flavored Markdown convention) before joining with ` | `. Centralising
// the escape here keeps every caller of MarkdownTable safe by default.
func joinCells(cells []string) string {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	return strings.Join(escaped, " | ")
}

// Resolve returns the concrete Format to use for w. When requested is
// FormatAuto: CI env non-empty → JSON, non-TTY → JSON, else → table. An
// unknown explicit Format returns an error mentioning the three valid
// values.
func Resolve(requested Format, w io.Writer) (Format, error) {
	switch requested {
	case FormatAuto:
		if os.Getenv("CI") != "" {
			return FormatJSON, nil
		}
		if !isTTY(w) {
			return FormatJSON, nil
		}
		return FormatTable, nil
	case FormatTable, FormatJSON, FormatMarkdown:
		return requested, nil
	default:
		return "", unsupportedFormat(requested)
	}
}

// unsupportedFormat is the single source of truth for the "we cannot
// give you that format" error. Both the unknown-format and nil-renderer
// paths route through here so user-visible wording stays uniform and
// ADR-0005 §"Renderer interface" stays in sync with the code.
func unsupportedFormat(f Format) error {
	return fmt.Errorf("unsupported --output %q (want table, json, or markdown)", string(f))
}
