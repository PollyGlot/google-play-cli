// Package list implements `gplay auth list`: print every registered
// Account with the active one marked. JSON output is supported via
// `--output json` and emits `{"accounts":[{"name":"...","active":true|false}, ...]}`.
package list

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/config"
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
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered Account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, output)
		},
	}
	cmd.Flags().StringVar(&output, "output", "table", "output format: table or json")
	return cmd
}

func run(cmd *cobra.Command, opts Options, output string) error {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	rows := make([]accountRow, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		rows = append(rows, accountRow{Name: a.Name, Active: a.Active})
	}

	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload{Accounts: rows})
	case "table":
		return renderTable(cmd.OutOrStdout(), rows)
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}

func renderTable(w io.Writer, rows []accountRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no accounts registered)")
		return err
	}
	for _, r := range rows {
		marker := "  "
		if r.Active {
			marker = "* "
		}
		if _, err := fmt.Fprintf(w, "%s%s\n", marker, r.Name); err != nil {
			return err
		}
	}
	return nil
}
