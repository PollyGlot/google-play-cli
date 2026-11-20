package listcmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const pinMarker = "✓"

type Input struct{}

type Payload struct {
	Apps []AppRow `json:"apps"`
}

type AppRow struct {
	Package string `json:"package"`
	Pinned  bool   `json:"pinned"`
}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

func Run(rc *kernel.RunContext, _ Input) (output.Renderable, error) {
	if rc.Resolved == nil {
		return nil, &authError{msg: "apps list: no resolved config; run `gplay auth login` to register an Account"}
	}
	account := rc.AccountName
	if account == "" {
		if err := rc.EnsureAccount(); err != nil {
			return nil, err
		}
		if rc.Account != nil {
			return nil, &exit.UsageError{Msg: "apps list: cannot list under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		account = rc.Resolved.ConfigAccount
	}
	if account == "" {
		return nil, kernel.NoAccountError()
	}
	if !accountInResolved(rc.Resolved.Accounts, account) {
		return nil, &authError{msg: fmt.Sprintf(
			"apps list: Account %q is not in the global config (keystore-only Accounts are unsupported); run `gplay auth login` to register it",
			account,
		)}
	}
	pkgs := registry.List(rc.Resolved.Accounts, account)
	rows := make([]AppRow, 0, len(pkgs))
	for _, p := range pkgs {
		rows = append(rows, AppRow{Package: p, Pinned: p == rc.Resolved.Pin})
	}
	return Payload{Apps: rows}, nil
}

func accountInResolved(accounts []config.Account, name string) bool {
	for _, a := range accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Apps) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Apps) },
	}
}

const emptyHint = "no packages registered. Run `gplay apps add <package>` to register one"

func renderTable(w io.Writer, rows []AppRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "(%s.)\n", emptyHint)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PACKAGE\tPINNED"); err != nil {
		return err
	}
	for _, r := range rows {
		mark := ""
		if r.Pinned {
			mark = pinMarker
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", r.Package, mark); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderMarkdown(w io.Writer, rows []AppRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "_No packages registered. Run `gplay apps add <package>` to register one._\n")
		return err
	}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		mark := ""
		if r.Pinned {
			mark = pinMarker
		}
		cells = append(cells, []string{r.Package, mark})
	}
	return output.MarkdownTable(w, []string{"Package", "Pinned"}, cells)
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List packages registered under the active Account",
		Long:  `List every Android package registered under the active Account in gplay's local registry. In table and markdown output the row matching the current repo's .gplay/config.json pin (if any) is marked with a ✓ in the Pinned column; in JSON output the same row carries "pinned": true. Pass no positional arguments: listing scope is always the active Account.`,
		Example: `  gplay apps list
  gplay apps list --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{})
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}
