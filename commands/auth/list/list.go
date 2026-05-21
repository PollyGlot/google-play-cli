// Package list implements `gplay auth list`: print every registered
// Account with the active one marked. Output Format is resolved by
// internal/output: TTY → table, pipe/CI → JSON (pass-through shape
// `{"accounts":[...]}`), and --output markdown returns a Markdown table.
package list

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Options pins where the command reads state from.
type Options struct {
	ConfigPath string
}

type accountRow struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type payload struct {
	Accounts []accountRow `json:"accounts"`
}

// NewCommand returns the cobra command for `gplay auth list`.
func NewCommand(opts Options) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered Account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, output.Format(outputFlag))
		},
	}
	cmd.Flags().StringVar(&outputFlag, "output", "", "output format: table, json, or markdown (default: auto — table on TTY, json in pipes/CI)")
	return cmd
}

func run(cmd *cobra.Command, opts Options, format output.Format) error {
	cfg, err := config.LoadGlobalOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	rows := make([]accountRow, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		rows = append(rows, accountRow{Name: a.Name, Active: a.Active})
	}
	return output.Render(cmd.OutOrStdout(), format, renderersFor(rows))
}

// renderersFor wires the three Format renderers for a payload of rows.
func renderersFor(rows []accountRow) output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, rows) },
		JSON:     func(w io.Writer) error { return writeJSON(w, payload{Accounts: rows}) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, rows) },
	}
}

func writeJSON(w io.Writer, p payload) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// Markers shown next to each row in `--output table` so the user can
// spot the active Account at a glance.
const (
	activeMarker   = "* "
	inactiveMarker = "  "
)

func renderTable(w io.Writer, rows []accountRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no accounts registered)")
		return err
	}
	for _, r := range rows {
		marker := inactiveMarker
		if r.Active {
			marker = activeMarker
		}
		if _, err := fmt.Fprintf(w, "%s%s\n", marker, r.Name); err != nil {
			return err
		}
	}
	return nil
}

// renderMarkdown emits a 2-column Markdown table (Account, Active). The
// Active cell carries "*" for the active Account and is otherwise empty
// — the same convention as the table renderer, just typeset for the
// idiom that fits a Markdown reader.
func renderMarkdown(w io.Writer, rows []accountRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "_No accounts registered._")
		return err
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		active := ""
		if r.Active {
			active = "*"
		}
		cells = append(cells, []string{r.Name, active})
	}
	return output.MarkdownTable(w, []string{"Account", "Active"}, cells)
}
