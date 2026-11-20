package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

type Format string

const (
	FormatAuto     Format = ""
	FormatTable    Format = "table"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

const EnvDefaultOutput = "GPLAY_DEFAULT_OUTPUT"

type IsTerminalFn func(w io.Writer) bool

var isTTY IsTerminalFn = defaultIsTTY

type fileDescriptor interface{ Fd() uintptr }

func defaultIsTTY(w io.Writer) bool {
	f, ok := w.(fileDescriptor)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func IsTerminalFunc() IsTerminalFn { return isTTY }

func SetIsTerminalFunc(fn IsTerminalFn) { isTTY = fn }

type Renderers struct {
	Table    func(io.Writer) error
	JSON     func(io.Writer) error
	Markdown func(io.Writer) error
}

type Renderable interface {
	Renderers() Renderers
}

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
	case FormatAuto:
	}
	if fn == nil {
		return unsupportedFormat(f)
	}
	if f == FormatJSON {
		return fn(w)
	}
	return renderHuman(w, fn)
}

func renderHuman(w io.Writer, fn func(io.Writer) error) error {
	sw := &sanitizingWriter{w: w}
	err := fn(sw)
	if ferr := sw.flush(); err == nil {
		err = ferr
	}
	return err
}

type sanitizingWriter struct {
	w    io.Writer
	line []byte
}

func (s *sanitizingWriter) Write(p []byte) (int, error) {
	s.line = append(s.line, p...)
	end := bytes.LastIndexByte(s.line, '\n')
	if end < 0 {
		return len(p), nil
	}
	_, err := io.WriteString(s.w, SanitizeText(string(s.line[:end+1])))
	s.line = append(s.line[:0], s.line[end+1:]...)
	return len(p), err
}

func (s *sanitizingWriter) flush() error {
	if len(s.line) == 0 {
		return nil
	}
	_, err := io.WriteString(s.w, SanitizeText(string(s.line)))
	s.line = nil
	return err
}

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func NonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func RegisterFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "output", "",
		"output format: table, json, or markdown (default: $GPLAY_DEFAULT_OUTPUT, else auto, meaning table on TTY and json in pipes/CI)")
}

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

func joinCells(cells []string) string {
	escaped := make([]string, len(cells))
	for i, c := range cells {
		escaped[i] = strings.ReplaceAll(sanitizeCell(c), "|", `\|`)
	}
	return strings.Join(escaped, " | ")
}

func Resolve(requested Format, w io.Writer) (Format, error) {
	switch requested {
	case FormatAuto:
		if env := os.Getenv(EnvDefaultOutput); env != "" {
			f := Format(env)
			switch f {
			case FormatTable, FormatJSON, FormatMarkdown:
				return f, nil
			default:
				return "", exit.Usagef("unsupported %s %q (want table, json, or markdown)", EnvDefaultOutput, env)
			}
		}
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

func unsupportedFormat(f Format) error {
	return exit.Usagef("unsupported --output %q (want table, json, or markdown)", string(f))
}
