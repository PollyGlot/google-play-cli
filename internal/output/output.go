// Package output owns Format selection and rendering dispatch for gplay
// commands. Every command supplies its three Renderers (table / json /
// markdown) and calls Render; this package resolves the requested Format
// against TTY state and the CI env var, then picks the matching Renderer.
//
// See docs/adr/0005-tty-aware-output.md for the design rationale and the
// rejected alternatives.
package output

import (
	"fmt"
	"io"
	"os"

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
// from r, and runs it. A nil field is reported as
// "unsupported --output <format> (this command does not support it)" so
// commands that ship without a Markdown renderer surface a uniform
// message instead of silently falling through.
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
		return fmt.Errorf("unsupported --output %q (this command does not support that format)", string(f))
	}
	return fn(w)
}

// MarkdownTable writes a standard GitHub-Flavored Markdown table: one
// header row, the `| --- | --- |` separator, then one row per data line.
// Cells are emitted verbatim — escaping pipe characters in user data is
// the caller's responsibility (no current cell value contains one).
func MarkdownTable(w io.Writer, headers []string, rows [][]string) error {
	if _, err := fmt.Fprintf(w, "| %s |\n", joinPipes(headers)); err != nil {
		return err
	}
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	if _, err := fmt.Fprintf(w, "| %s |\n", joinPipes(sep)); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "| %s |\n", joinPipes(row)); err != nil {
			return err
		}
	}
	return nil
}

func joinPipes(cells []string) string {
	out := ""
	for i, c := range cells {
		if i > 0 {
			out += " | "
		}
		out += c
	}
	return out
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
		return "", fmt.Errorf("unsupported --output %q (want table, json, or markdown)", string(requested))
	}
}
