// Package accessiblecmd implements `gplay apps accessible list`: the
// server-authoritative enumeration of the Apps the active credential can
// access, via the Play Developer Reporting `apps.search` method (#347,
// ADR-0039).
//
// This is deliberately NOT `apps list`. `apps list` prints gplay's local
// registry — the chosen working set of packages the operator has run
// `apps add` on. `apps accessible list` asks the server "which Apps can
// this credential see?". The two do not coincide by design (ADR-0039): a
// service account may hold androidpublisher rights on an App without the
// Reporting access that surfaces it here, and may see hundreds of org Apps
// it does not drive. `accessible list` is the bootstrap-discovery surface
// whose output feeds `apps add`; the registry stays the source of truth for
// the working set. Hence a new noun (`accessible`) under a canonical verb
// (`list`), never a `--source` flag on `apps list` and never an `apps
// discover` verb (ADR-0019, ADR-0039).
//
// The read rides the Play Developer Reporting service: a distinct host and
// the least-privilege token.ReportingScope, annotated via kernel.WithScope
// on the list command (the same path `vitals` takes). Pagination is
// caller-driven — one page per invocation with --page-size/--page-token,
// the device-tiers/games convention — and --output json passes the
// SearchAccessibleAppsResponse through verbatim, nextPageToken included
// (ADR-0003).
package accessiblecmd

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
)

// Input is the request-shaped struct cobra builds from flags. There is no
// --package: apps.search is account-scoped, it enumerates every accessible
// App, not one app's sub-resource.
type Input struct {
	PageSize  int
	PageToken string
}

// usageError is a CLI-misuse error (a negative --page-size); ExitCode()=2
// per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Payload renders one page of accessible Apps. Raw is the verbatim
// apps.search body for the ADR-0003 JSON pass-through; Apps drives the
// table/markdown views.
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

// Run resolves an authenticated (reporting-scoped) client and fetches one
// page of accessible Apps. When the page carries a nextPageToken, a note on
// stderr tells the operator how to fetch the next page — the table/markdown
// views would otherwise silently under-report; --output json keeps the
// token in the body for a machine caller.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.PageSize < 0 {
		return nil, &usageError{msg: "apps accessible list: invalid --page-size: must be >= 0"}
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	sr, raw, err := accessibleapps.Search(rc.Ctx, hc, in.PageSize, in.PageToken)
	if err != nil {
		return nil, err
	}
	if sr.NextPageToken != "" && rc.Stderr != nil {
		_, _ = io.WriteString(rc.Stderr, "NOTE: more Apps available — re-run with --page-token "+sr.NextPageToken+" for the next page.\n")
	}
	return Payload{Apps: sr.Apps, Raw: raw}, nil
}

// newListCommand returns the `apps accessible list` verb.
func newListCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the Apps the active credential can access (server-authoritative)",
		Long: `List the Apps the active credential can access, straight from Google via the
Play Developer Reporting apps.search method — a server-authoritative
inventory, not gplay's local registry.

This is distinct from ` + "`gplay apps list`" + `, which prints the packages you
have run ` + "`gplay apps add`" + ` on (your chosen working set). The two sets do
not necessarily coincide: a credential may be able to add an App it cannot
see here, or see org Apps it does not drive (ADR-0039). Use this to
bootstrap — discover package names, then ` + "`gplay apps add`" + ` the ones you
want to work on.

Pagination is one page per invocation: use --page-size and --page-token,
and --output json passes the SearchAccessibleAppsResponse through verbatim,
nextPageToken included (ADR-0003). In table/markdown output a note on stderr
carries the next --page-token when more Apps are available.

Reads on the Play Developer Reporting service with the least-privilege
reporting scope; needs a resolved credential (no local-registry fallback).`,
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

// NewCommand returns the cobra group for `apps accessible`: a pure grouping
// noun (ADR-0019). It currently holds one verb, `list`; the bare
// `accessible` command only prints help (kernel.GroupRunE), so a mistyped
// subcommand fails as CLI misuse (exit 2) rather than silently printing
// help with exit 0. The `list` verb is annotated with the reporting scope
// (kernel.WithScope) so it mints a least-privilege playdeveloperreporting
// token.
func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accessible",
		Short: "Discover the Apps the active credential can access (server-authoritative)",
		Long: `Group for server-authoritative App discovery via the Play Developer
Reporting API. ` + "`apps accessible list`" + ` enumerates the Apps the active
credential can access; the bare ` + "`apps accessible`" + ` command prints this
help.

Distinct from ` + "`apps list`" + ` (gplay's local registry) — see
` + "`apps accessible list --help`" + ` and ADR-0039.`,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(kernel.WithScope(newListCommand(boot), token.ReportingScope))
	return cmd
}
