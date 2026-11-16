// Package errorscmd implements `gplay vitals errors`: the error-reporting
// surface of the Play Developer Reporting API (#49). Three read-only views:
//
//   - counts : errors.counts.query: the errorReportCount/distinctUsers metric
//     set (a timeline, reusing the metric-set query machinery).
//   - issues : errors.issues.search: clustered error issues.
//   - reports: errors.reports.search: individual reports, i.e. the stack traces.
//
// Stack frames come back OBFUSCATED until ProGuard/R8 mappings are uploaded
// (#250, `releases mappings upload`); that is a documented degradation, not a
// blocker: these commands work today, the frames are just not symbolicated. A
// stderr note says so on the issues/reports views.
package errorscmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// mappingsNote documents the obfuscation degradation (#250) on the views that
// carry stack frames.
// The help text quotes the line as stderr shows it, hence the two constants.
const (
	mappingsNoteBody = "stack frames are obfuscated until you upload ProGuard/R8 mappings (gplay releases mappings upload); until then frames are not symbolicated."
	mappingsNote     = "NOTE: " + mappingsNoteBody
)

// NewCommand returns the `gplay vitals errors` group. Every leaf is wrapped with
// kernel.WithScope so it mints a least-privilege playdeveloperreporting token,
// like every other vitals command.
func NewCommand(boot kernel.Boot) *cobra.Command {
	group := &cobra.Command{
		Use:           "errors",
		Short:         "Read crash/ANR error reports: counts, clustered issues, and individual reports",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	group.AddCommand(kernel.WithScope(newCountsCommand(boot), token.ReportingScope))
	group.AddCommand(kernel.WithScope(newIssuesCommand(boot), token.ReportingScope))
	group.AddCommand(kernel.WithScope(newReportsCommand(boot), token.ReportingScope))
	return group
}

// --- shared helpers --------------------------------------------------------

// windowHelp and sinceHelp state the window granularity in each search view's
// --help.
const (
	windowHelp = `--since sets the window. A window in days (7d) covers whole UTC days ending
today at 00:00 UTC. Any other window (6h, 24h) is counted in whole hours back
from the newest hour of error data (the errorCount set's HOURLY freshness).`
	sinceHelp = "window length: whole UTC days (e.g. 28d) or whole hours back from the newest data (e.g. 6h)"
)

// inHours reports whether a --since spec asks for an hour window: anything but
// a day count, the flag default included.
func inHours(since string) bool {
	s := strings.TrimSpace(since)
	return s != "" && !strings.HasSuffix(s, "d")
}

// window resolves a parsed --since into the [Start, End) search interval. A
// day window ends today at 00:00 UTC, date-only. An hour window ends on the
// current hour, capped at the errorCount set's HOURLY freshness: the newest
// hour the reporting pipeline has published, read through the same `.get` the
// metric-set queries anchor on (#768, #775).
func window(rc *kernel.RunContext, hc *http.Client, pkg string, since time.Duration, hourly bool) (vitals.SearchOptions, error) {
	now := time.Now().UTC()
	if !hourly {
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return vitals.SearchOptions{Start: end.Add(-since), End: end}, nil
	}
	end := now.Truncate(time.Hour)
	latest, err := vitalscmd.LatestEnd(rc, hc, vitals.ErrorCountSet(), pkg, "HOURLY")
	if err != nil {
		return vitals.SearchOptions{}, err
	}
	if !latest.IsZero() && latest.Before(end) {
		end = latest
	}
	return vitals.SearchOptions{Start: end.Add(-since), End: end, Hourly: true}, nil
}

// emptyWarn is the stderr line for an empty result; for a non-empty one the
// mappings note is emitted instead.
func warnResult(rc *kernel.RunContext, kind string, n int) {
	if n == 0 {
		rc.Warnf("no error %s in the requested window; vitals are reported with a delay, so an empty window is not the same as zero.", kind)
		return
	}
	rc.Notef("%s", mappingsNoteBody)
}

// --- counts ----------------------------------------------------------------

type countsInput struct {
	Package, By, VersionCode, Since, Period string
	Describe                                bool     // --describe: errors.counts.get (freshness) instead of :query
	WindowFlags                             []string // query-shaping flags the user set (rejected under --describe)
}

// countsWindowFlags are the counts flags that only make sense for a `:query`.
var countsWindowFlags = []string{"since", "period", "by", "version-code"}

// runCounts queries the errorCount metric set, reusing the shared metric-set
// orchestration (the errors.counts set is queryable like any rate set). Note
// errorCount does not support every --by dimension (no countryCode), so the
// set-aware PresetParams rejects `--by country` with the valid set for errors.
// --describe reaches the set's `.get` through the same shared body (#545).
func runCounts(rc *kernel.RunContext, in countsInput) (output.Renderable, error) {
	if err := vitalscmd.RejectWindowFlags(in.Describe, in.WindowFlags); err != nil {
		return nil, err
	}
	if in.Describe {
		return vitalscmd.Describe(rc, vitals.ErrorCountSet(), in.Package)
	}
	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, err
	}
	p, err := vitalscmd.PresetParams(idx, vitals.ErrorCountSet(), in.Package, in.VersionCode, in.By, in.Since, in.Period)
	if err != nil {
		return nil, err
	}
	return vitalscmd.Execute(rc, p)
}

func newCountsCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         countsInput
	)
	cmd := &cobra.Command{
		Use:   "counts",
		Short: "Error report counts over a window (errorCount metric set)",
		Long: `Query the errorCount metric set (errorReportCount / distinctUsers) as a
timeline, the count side of vitals errors.

--by slices the timeline (` + vitalscmd.ByChoices() + `); --version-code filters to
one versionCode. Read-only; --output json mirrors the API response verbatim.
--describe fetches the metric set's descriptor instead (latest available end
time per aggregation period); the window flags do not apply and are rejected.`,
		Example: `  gplay vitals errors counts --package com.example.app
  gplay vitals errors counts --by versionCode --since 7d
  gplay vitals errors counts --describe`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in.WindowFlags = vitalscmd.ChangedFlags(cmd, countsWindowFlags...)
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return runCounts(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.By, "by", "", "slice the timeline by a dimension ("+vitalscmd.ByChoices()+"; availability depends on the metric set)")
	cmd.Flags().StringVar(&in.VersionCode, vitalscmd.VersionCodeFlag, "", "filter to a single versionCode")
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, "window length back from now, e.g. 28d or 24h")
	cmd.Flags().StringVar(&in.Period, "period", vitalscmd.DefaultPeriod, vitalscmd.PeriodHelp(vitals.ErrorCountSet()))
	cmd.Flags().BoolVar(&in.Describe, vitalscmd.DescribeFlag, false, vitalscmd.DescribeHelp)
	vitalscmd.RegisterVersionCodeCompletion(cmd, boot)
	return cmd
}

// --- issues ----------------------------------------------------------------

type issuesInput struct {
	Package, Since, Filter, OrderBy string
	Limit, SampleReports            int
}

// issuesPayload renders clustered error issues. JSON is the verbatim API
// pass-through (ADR-0003).
type issuesPayload struct {
	Raw    json.RawMessage
	Issues []vitals.ErrorIssue
}

var issueColumns = []output.Column[vitals.ErrorIssue]{
	{Key: "type", Header: "TYPE", Value: func(i vitals.ErrorIssue) string { return i.Type }},
	{Key: "cause", Header: "CAUSE", Value: func(i vitals.ErrorIssue) string { return clip(i.Cause, 40) }},
	{Key: "location", Header: "LOCATION", Value: func(i vitals.ErrorIssue) string { return clip(i.Location, 40) }},
	{Key: "reports", Header: "REPORTS", Value: func(i vitals.ErrorIssue) string { return i.ReportCount }},
	{Key: "users", Header: "USERS", Value: func(i vitals.ErrorIssue) string { return i.DistinctUser }},
	{Key: "last_seen", Header: "LAST_SEEN", Value: func(i vitals.ErrorIssue) string { return i.LastSeen }},
}

func (p issuesPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, issueColumns, p.Issues) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, issueColumns, p.Issues) },
	}
}

func runIssues(rc *kernel.RunContext, in issuesInput) (output.Renderable, error) {
	if in.SampleReports < 0 || in.SampleReports > vitals.MaxSampleReports {
		return nil, exit.Usagef("invalid --sample-reports %d: Play attaches 0 to %d sample reports per issue", in.SampleReports, vitals.MaxSampleReports)
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	since, err := vitalscmd.ParseSince(in.Since)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	opts, err := window(rc, hc, pkg, since, inHours(in.Since))
	if err != nil {
		return nil, err
	}
	opts.Filter, opts.Limit, opts.OrderBy, opts.SampleReports = in.Filter, in.Limit, in.OrderBy, in.SampleReports
	raw, truncated, err := vitals.SearchErrorIssues(rc.Ctx, hc, pkg, opts)
	if err != nil {
		return nil, err
	}
	issues, err := vitals.ParseErrorIssues(raw)
	if err != nil {
		return nil, err
	}
	warnResult(rc, "issues", len(issues))
	if truncated {
		rc.WarnTruncated(len(issues), "issues", "limit")
	}
	return issuesPayload{Raw: raw, Issues: issues}, nil
}

func newIssuesCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         issuesInput
	)
	cmd := &cobra.Command{
		Use:   "issues",
		Short: "List clustered error issues (crashes/ANRs grouped by cause)",
		Long: `Search the clustered error issues: crashes and ANRs grouped by cause and
location: over a window.

` + windowHelp + `

` + mappingsNote + `

--sample-reports 1 attaches the name of one sample error report to each issue
(its sampleErrorReports field, in --output json), which saves a second
vitals errors reports call to find a stack trace. Play supports 0 or 1 today.

Read-only; --output json mirrors the API response verbatim.`,
		Example: `  gplay vitals errors issues --package com.example.app

  # One sample report per issue, to jump straight to a stack trace
  gplay vitals errors issues --sample-reports 1 --output json

  # The 20 most frequent crash clusters of the last week
  gplay vitals errors issues --filter "errorIssueType = CRASH" --order-by "errorReportCount desc" \
    --since 7d --limit 20`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return runIssues(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, sinceHelp)
	cmd.Flags().StringVar(&in.Filter, "filter", "", "AIP-160 filter, e.g. \"errorIssueType = CRASH\" or \"versionCode = 123\"")
	cmd.Flags().StringVar(&in.OrderBy, "order-by", "", "sort order, e.g. \"errorReportCount desc\"")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "max issues to return (0 = all, no cap); a capped list warns on stderr")
	cmd.Flags().IntVar(&in.SampleReports, "sample-reports", 0, "sample error reports to attach to each issue (0 or 1)")
	return cmd
}

// --- reports ---------------------------------------------------------------

type reportsInput struct {
	Package, Since, Filter string
	Limit                  int
}

// reportsPayload renders individual error reports (the stack traces).
type reportsPayload struct {
	Raw     json.RawMessage
	Reports []vitals.ErrorReport
}

var reportColumns = []output.Column[vitals.ErrorReport]{
	{Key: "event_time", Header: "EVENT_TIME", Value: func(r vitals.ErrorReport) string { return r.EventTime }},
	{Key: "type", Header: "TYPE", Value: func(r vitals.ErrorReport) string { return r.Type }},
	{Key: "version", Header: "VERSION", Value: func(r vitals.ErrorReport) string { return r.AppVersion }},
	{Key: "device", Header: "DEVICE", Value: func(r vitals.ErrorReport) string { return r.Device }},
	{Key: "api", Header: "API", Value: func(r vitals.ErrorReport) string { return r.OsAPILevel }},
	{Key: "report", Header: "REPORT", Value: func(r vitals.ErrorReport) string { return clip(firstLine(r.Report), 60) }},
}

func (p reportsPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, reportColumns, p.Reports) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, reportColumns, p.Reports) },
	}
}

func runReports(rc *kernel.RunContext, in reportsInput) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	since, err := vitalscmd.ParseSince(in.Since)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	opts, err := window(rc, hc, pkg, since, inHours(in.Since))
	if err != nil {
		return nil, err
	}
	opts.Filter, opts.Limit = in.Filter, in.Limit
	raw, truncated, err := vitals.SearchErrorReports(rc.Ctx, hc, pkg, opts)
	if err != nil {
		return nil, err
	}
	reports, err := vitals.ParseErrorReports(raw)
	if err != nil {
		return nil, err
	}
	warnResult(rc, "reports", len(reports))
	if truncated {
		rc.WarnTruncated(len(reports), "reports", "limit")
	}
	return reportsPayload{Raw: raw, Reports: reports}, nil
}

func newReportsCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         reportsInput
	)
	cmd := &cobra.Command{
		Use:   "reports",
		Short: "List individual error reports (the stack traces)",
		Long: `Search individual error reports (the platform-produced stack traces) over a
window.

` + windowHelp + `

` + mappingsNote + `

The full report text is in --output json; the table shows the first line.
Read-only; --output json mirrors the API response verbatim.`,
		Example: `  gplay vitals errors reports --package com.example.app

  # Full stack traces of one versionCode, as JSON
  gplay vitals errors reports --filter "versionCode = 1042" --since 7d --limit 10 --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return runReports(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Since, "since", vitalscmd.DefaultSince, sinceHelp)
	cmd.Flags().StringVar(&in.Filter, "filter", "", "AIP-160 filter, e.g. \"versionCode = 123\"")
	cmd.Flags().IntVar(&in.Limit, "limit", 0, "max reports to return (0 = all, no cap); a capped list warns on stderr")
	return cmd
}

// --- small helpers ---------------------------------------------------------

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
