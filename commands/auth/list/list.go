// Package list implements `gplay auth list`: print every registered
// Account with the active one marked. Output Format is resolved by
// internal/output: TTY → table, pipe/CI → JSON (pass-through shape
// `{"accounts":[...]}`), and --output markdown returns a Markdown table.
package list

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Input is the business surface of `gplay auth list`. The kernel hands
// us the IO streams and the config path; this struct only carries the
// requested output Format.
type Input struct {
	Format output.Format
}

// Payload is what the renderers consume. Exported so tests can dial in
// expected rows without going through cobra.
type Payload struct {
	Accounts []AccountRow `json:"accounts"`
}

// AccountRow is one row of the list.
type AccountRow struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// Run is the pure business function: load the global registry, render
// it to rc.Stdout. No cobra, no t.TempDir required to exercise.
func Run(rc *kernel.RunContext, in Input) error {
	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return err
	}
	rows := make([]AccountRow, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		rows = append(rows, AccountRow{Name: a.Name, Active: a.Active})
	}
	return output.Render(rc.Stdout, in.Format, renderersFor(rows))
}

// NewCommand returns the cobra command for `gplay auth list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered Account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(kernel.FromCobra(cmd, boot), Input{Format: output.Format(outputFlag)})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}

// renderersFor wires the three Format renderers for a payload of rows.
func renderersFor(rows []AccountRow) output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, rows) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, Payload{Accounts: rows}) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, rows) },
	}
}

// Markers shown next to each row in `--output table` so the user can
// spot the active Account at a glance.
const (
	activeMarker   = "* "
	inactiveMarker = "  "
)

func renderTable(w io.Writer, rows []AccountRow) error {
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
func renderMarkdown(w io.Writer, rows []AccountRow) error {
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
