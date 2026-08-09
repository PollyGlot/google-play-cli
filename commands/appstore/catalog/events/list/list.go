// Package list implements `gplay appstore catalog events list --start-time <t>
// --end-time <t>`: one page of update events for the apps eligible for an app
// store's catalog (appstorecatalog.recentupdateevents.list).
//
// This is the incremental-sync half of the Catalog Export for app stores: an
// operator polls the feed for a time range and reacts per event — re-fetch a
// MODIFICATION with `appstore catalog view`, delist a DELETION. Both time
// bounds are required by the API and validated client-side as RFC 3339 so a
// malformed range fails fast (exit 2) instead of costing a round trip.
// Pagination is caller-driven — one page per invocation with
// --page-size/--page-token, the accessible-apps / device-tiers convention.
// Read-only, Edit-free; --output json is the ListRecentUpdateEventsResponse
// verbatim (ADR-0003), nextPageToken included. Ships [experimental].
package list

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	StorePackage string
	StartTime    string
	EndTime      string
	PageSize     int
	PageToken    string
}

// columns is the update-event table (ADR-0018 shared machinery): which Play app
// changed, when, and how.
var columns = []output.Column[appstorecatalog.RecentUpdateEvent]{
	{Key: "package", Header: "PACKAGE", Value: func(e appstorecatalog.RecentUpdateEvent) string { return e.PlayAppPackageName }},
	{Key: "time", Header: "TIME", Value: func(e appstorecatalog.RecentUpdateEvent) string { return e.EventTime }},
	{Key: "type", Header: "TYPE", Value: func(e appstorecatalog.RecentUpdateEvent) string { return e.UpdateType }},
}

// Payload satisfies output.Renderable. Events drives the table/markdown views;
// Raw is the verbatim ListRecentUpdateEventsResponse for the ADR-0003 JSON
// pass-through, nextPageToken included.
type Payload struct {
	Events []appstorecatalog.RecentUpdateEvent
	Raw    json.RawMessage
}

// renderJSON emits the verbatim API bytes (ADR-0003 pass-through). An empty Raw
// is a bug, never a valid render: erroring loudly beats zero bytes at exit 0 on
// the CI default format.
func (p Payload) renderJSON(w io.Writer) error {
	if len(p.Raw) == 0 {
		return fmt.Errorf("missing raw update events payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, columns, p.Events) },
		JSON:     p.renderJSON,
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, columns, p.Events) },
	}
}

// Run is the business function the kernel invokes. It validates the required
// time range and the page size client-side, resolves the app store package name
// from the flag/env cascade, then fetches one page. When the page carries a
// nextPageToken a note on stderr tells the operator how to fetch the next page —
// the human views would otherwise silently under-report an incremental sync;
// --output json keeps the token in the body for a machine caller.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	start, end, err := appstorecmd.ValidateTimeRange(in.StartTime, in.EndTime)
	if err != nil {
		return nil, err
	}
	if in.PageSize < 0 {
		return nil, appstorecmd.Usagef("invalid --page-size: must be >= 0 (0 lets the server apply its default of %d)", appstorecatalog.DefaultPageSize)
	}
	storePkg, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	resp, raw, err := appstorecatalog.ListRecentUpdateEvents(rc.Ctx, httpClient, storePkg, start, end, in.PageSize, in.PageToken)
	if err != nil {
		return nil, appstorecmd.ClassifyStoreRead(storePkg, err)
	}
	if resp.NextPageToken != "" && rc.Stderr != nil {
		_, _ = io.WriteString(rc.Stderr, "NOTE: more update events available — re-run with --page-token "+resp.NextPageToken+" (keeping the same --start-time/--end-time/--page-size: the API rejects a page token when any other parameter changes) for the next page.\n")
	}
	return Payload{Events: resp.RecentUpdateEvents, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay appstore catalog events list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog update events in a time range (incremental catalog sync)",
		Long: `List the update events for the apps eligible for an app store's catalog, in
a time range (appstorecatalog.recentupdateevents.list).

This is the incremental-sync feed of the Google Play Catalog Export for app
stores. Each event names the Play app that changed, when it changed, and the
kind of change:

  MODIFICATION  the app changed — re-fetch it with
                ` + "`gplay appstore catalog view <play-package>`" + `
  DELETION      the app stopped being eligible for catalog inclusion or was
                removed from the Play Store — delist it

--start-time and --end-time are BOTH REQUIRED by the API and are validated
client-side as RFC 3339 timestamps (e.g. 2026-07-01T00:00:00Z or
2026-07-01T02:00:00+02:00). The range is [start, end) — start inclusive, end
exclusive — so an --end-time at or before --start-time is a usage error
(exit 2), caught before any network call so CI fails fast.

Addressing rides the app store package name, not the repo's .gplay/config.json
pin: pass --store-package <pkg> or export ` + appstorecmd.EnvStorePackage + `.

Pagination is one page per invocation. --page-size defaults to the server's
` + strconv.Itoa(appstorecatalog.DefaultPageSize) + ` when unset, and anything above ` + strconv.Itoa(appstorecatalog.MaxPageSize) + ` is coerced down to it; pass the
previous response's nextPageToken as --page-token, keeping the SAME
--start-time/--end-time AND --page-size across pages (the API rejects a page
token when any other parameter changes). In table/markdown output a note on stderr
carries the next --page-token when more events are available; --output json
passes the ListRecentUpdateEventsResponse through verbatim (ADR-0003),
nextPageToken included. stdout carries the data, stderr the logs.

This is a direct read outside the Edit model — it opens no Edit.`,
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
	cmd.Flags().StringVar(&in.StorePackage, "store-package", "",
		"package name of the app store on whose behalf the request is made (or $"+appstorecmd.EnvStorePackage+")")
	cmd.Flags().StringVar(&in.StartTime, "start-time", "",
		"start of the range, inclusive — RFC 3339, e.g. 2026-07-01T00:00:00Z (required)")
	cmd.Flags().StringVar(&in.EndTime, "end-time", "",
		"end of the range, exclusive — RFC 3339, e.g. 2026-07-08T00:00:00Z (required)")
	cmd.Flags().IntVar(&in.PageSize, "page-size", 0,
		"max update events per page (server default "+strconv.Itoa(appstorecatalog.DefaultPageSize)+" when unset; coerced down to "+strconv.Itoa(appstorecatalog.MaxPageSize)+")")
	cmd.Flags().StringVar(&in.PageToken, "page-token", "", "page token from a previous response's nextPageToken")
	return cmd
}
