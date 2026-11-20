package accessiblecmd

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
)

type Input struct {
	PageSize  int
	PageToken string
}

type Payload struct {
	Apps []accessibleapps.App
	Raw  json.RawMessage
}

var columns = []output.Column[accessibleapps.App]{
	{Key: "package", Header: "PACKAGE", Value: func(a accessibleapps.App) string { return a.PackageName }},
	{Key: "display_name", Header: "DISPLAY_NAME", Value: func(a accessibleapps.App) string { return a.DisplayName }},
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, columns, p.Apps) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, columns, p.Apps) },
	}
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.PageSize < 0 {
		return nil, &exit.UsageError{Msg: "apps accessible list: invalid --page-size: must be >= 0"}
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	sr, raw, err := accessibleapps.Search(rc.Ctx, hc, in.PageSize, in.PageToken)
	if err != nil {
		return nil, err
	}
	if sr.NextPageToken != "" {
		rc.Notef("more Apps available, re-run with --page-token %s for the next page.", sr.NextPageToken)
	}
	return Payload{Apps: sr.Apps, Raw: raw}, nil
}

func newListCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Apps the active credential can access (server-authoritative)",
		Long: `List the Apps the active credential can access, straight from Google via the
Play Developer Reporting apps.search method: a server-authoritative
inventory, not gplay's local registry.

This is distinct from ` + "`gplay apps list`" + `, which prints the packages you
have run ` + "`gplay apps add`" + ` on (your chosen working set). The two sets do
not necessarily coincide: a credential may be able to add an App it cannot
see here, or see org Apps it does not drive. Use this to
bootstrap: discover package names, then ` + "`gplay apps add`" + ` the ones you
want to work on.

Pagination is one page per invocation: use --page-size and --page-token,
and --output json passes the SearchAccessibleAppsResponse through verbatim,
nextPageToken included. In table/markdown output a note on stderr
carries the next --page-token when more Apps are available.

Reads on the Play Developer Reporting service with the least-privilege
reporting scope; needs a resolved credential (no local-registry fallback).`,
		Example: `  # Discover the package names the active credential can see
  gplay apps accessible list

  # Page through a large Developer account, 200 Apps at a time
  gplay apps accessible list --page-size 200 --output json | jq -r '.apps[].packageName'`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().IntVar(&in.PageSize, "page-size", 0, "max Apps per page (API default 50 when unset; capped at 1000 by the server)")
	cmd.Flags().StringVar(&in.PageToken, "page-token", "", "page token from a previous response's nextPageToken")
	return cmd
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accessible",
		Short: "Discover the Apps the active credential can access (server-authoritative)",
		Long: `Group for server-authoritative App discovery via the Play Developer
Reporting API. ` + "`apps accessible list`" + ` enumerates the Apps the active
credential can access; the bare ` + "`apps accessible`" + ` command prints this
help.

Distinct from ` + "`apps list`" + ` (gplay's local registry): see
` + "`apps accessible list --help`" + `.`,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(kernel.WithScope(newListCommand(boot), token.ReportingScope))
	return cmd
}
