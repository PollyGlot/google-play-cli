// Package list implements `gplay auth list`: print every registered
// Account with the active one marked. Output Format is resolved by
// internal/output: TTY → table, pipe/CI → JSON (pass-through shape
// `{"accounts":[...]}`), and --output markdown returns a Markdown table.
package list

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Input has no command-specific flags; the kernel hands list everything
// it needs via rc.Resolved.
type Input struct{}

// Payload is the rendered data. The kernel calls Renderers() and dispatches.
type Payload struct {
	Accounts []AccountRow `json:"accounts"`
}

// AccountRow is one row of the list.
type AccountRow struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Accounts) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Accounts) },
	}
}

// Run is the pure business function: read the registry from rc.Resolved,
// shape it into rows, return a Renderable.
func Run(rc *kernel.RunContext, _ Input) (output.Renderable, error) {
	rows := make([]AccountRow, 0, len(rc.Resolved.Accounts))
	for _, a := range rc.Resolved.Accounts {
		rows = append(rows, AccountRow{Name: a.Name, Active: a.Active})
	}
	return Payload{Accounts: rows}, nil
}

// NewCommand returns the cobra command for `gplay auth list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered Account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{})
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}

// Markers shown next to each row in `--output table`.
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
