// Package query implements `gplay vitals query <metric-set>`: the generic,
// read-only escape hatch over the Play Developer Reporting API metric sets
// (ADR-0026 full coverage). It is the walking skeleton the opinionated presets
// (`vitals crashes`, `vitals anr`, …) are thin wrappers over (#49).
//
// Metrics, dimensions and the aggregation period are validated OFFLINE against
// the embedded Schema index — never invented (the catalog is read from the
// snapshot). Output is the ADR-0003 JSON pass-through on stdout; table/markdown
// render the timeline (dates × metrics, sliced by the requested dimensions).
// A freshness note is always written to stderr so an empty window is never
// mistaken for "zero crashes".
package query

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// Defaults mirror the Play Console default view (#49).
const (
	defaultSince  = "28d"
	defaultPeriod = "DAILY"
)

// Input is the request-shaped struct cobra builds from flags and the
// metric-set argument.
type Input struct {
	MetricSet  string   // positional arg: crashrate, anrrate, …
	Package    string   // --package
	Metrics    []string // --metrics (empty → the set's primary metric)
	Dimensions []string // --dimensions (empty → no slicing, one row per period)
	Period     string   // --period DAILY|HOURLY|FULL_RANGE
	Since      string   // --since 28d, 24h, …
}

// Payload is the rendered result: the verbatim API response for the JSON
// pass-through, plus the parsed Timeline and the resolved column order
// (dimensions then metrics) for table/markdown.
type Payload struct {
	Raw        json.RawMessage
	Timeline   vitals.Timeline
	Dimensions []string
	Metrics    []string
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	cols := p.columns()
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, cols, p.Timeline.Rows) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, cols, p.Timeline.Rows) },
	}
}

// columns builds the timeline's display columns: DATE first, then one column
// per requested dimension (the slice axis), then one per requested metric.
func (p Payload) columns() []output.Column[vitals.Row] {
	cols := []output.Column[vitals.Row]{
		{Key: "date", Header: "DATE", Value: func(r vitals.Row) string { return r.Date }},
	}
	for _, d := range p.Dimensions {
		d := d
		cols = append(cols, output.Column[vitals.Row]{
			Key: d, Header: strings.ToUpper(d), Value: func(r vitals.Row) string { return r.Dimensions[d] },
		})
	}
	for _, m := range p.Metrics {
		m := m
		cols = append(cols, output.Column[vitals.Row]{
			Key: m, Header: m, Value: func(r vitals.Row) string { return r.Metrics[m] },
		})
	}
	return cols
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	set, ok := vitals.MetricSetByName(in.MetricSet)
	if !ok {
		return nil, exit.Usagef("unknown metric set %q (valid: %s)", in.MetricSet, strings.Join(vitals.MetricSetNames(), ", "))
	}

	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package — pass --package <pkg> or run gplay init in your repo")
	}

	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, fmt.Errorf("load embedded schema index: %w", err)
	}

	period := in.Period
	if period == "" {
		period = defaultPeriod
	}
	period = strings.ToUpper(period)
	if err := validateOne("period", period, vitals.SupportedPeriods(idx)); err != nil {
		return nil, err
	}

	metrics := in.Metrics
	supportedMetrics := vitals.SupportedMetrics(idx, set)
	if len(metrics) == 0 {
		if len(supportedMetrics) == 0 {
			return nil, fmt.Errorf("metric set %q has no metrics in the index", set.Name)
		}
		metrics = []string{supportedMetrics[0]} // the set's primary metric
	}
	if err := validateAll("metric", metrics, supportedMetrics, set.Name); err != nil {
		return nil, err
	}

	dimensions := in.Dimensions
	if err := validateAll("dimension", dimensions, vitals.SupportedDimensions(idx, set), set.Name); err != nil {
		return nil, err
	}

	since, err := parseSince(in.Since)
	if err != nil {
		return nil, err
	}

	body, err := buildBody(metrics, dimensions, period, since, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := vitals.Query(rc.Ctx, httpClient, set, pkg, body)
	if err != nil {
		return nil, err
	}

	tl, err := vitals.ParseTimeline(raw)
	if err != nil {
		return nil, err
	}

	warnFreshness(rc, set, tl)

	return Payload{Raw: raw, Timeline: tl, Dimensions: dimensions, Metrics: metrics}, nil
}

// validateOne rejects a single value not present in allowed, with the valid set
// in the message — the "never invented" guard for --period.
func validateOne(kind, value string, allowed []string) error {
	for _, a := range allowed {
		if a == value {
			return nil
		}
	}
	return exit.Usagef("unknown %s %q (valid: %s)", kind, value, strings.Join(allowed, ", "))
}

// validateAll rejects any value not present in allowed, naming the metric set
// so the user knows where to look. allowed comes straight from the snapshot.
func validateAll(kind string, values, allowed []string, set string) error {
	for _, v := range values {
		found := false
		for _, a := range allowed {
			if a == v {
				found = true
				break
			}
		}
		if !found {
			sorted := append([]string(nil), allowed...)
			sort.Strings(sorted)
			return exit.Usagef("unknown %s %q for %s (valid: %s)", kind, v, set, strings.Join(sorted, ", "))
		}
	}
	return nil
}

// parseSince turns a window spec into a duration: "Nd" (days) or any Go
// duration ("24h", "90m"); empty defaults to 28d. A non-positive or malformed
// value is CLI misuse.
func parseSince(spec string) (time.Duration, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		s = defaultSince
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days <= 0 {
			return 0, exit.Usagef("invalid --since %q (want e.g. 28d, 24h)", spec)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, exit.Usagef("invalid --since %q (want e.g. 28d, 24h)", spec)
	}
	return d, nil
}

// dateTime is the google.type.DateTime subset gplay sends. For DAILY the time
// fields are left unset (the API requires it) and the timezone is left unset so
// the metric set's default applies (America/Los_Angeles for DAILY, UTC for
// HOURLY) — see the TimelineSpec prose in the snapshot.
type dateTime struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
	Hours int `json:"hours,omitempty"`
}

// buildBody assembles the `:query` request body for the given window. endTime is
// the period boundary at/after now (exclusive); startTime is endTime minus the
// window. HOURLY carries the hour field; DAILY/FULL_RANGE are date-only.
func buildBody(metrics, dimensions []string, period string, since time.Duration, now time.Time) ([]byte, error) {
	hourly := period == "HOURLY"
	var end, start time.Time
	if hourly {
		end = now.Truncate(time.Hour)
	} else {
		end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	start = end.Add(-since)

	body := map[string]any{
		"metrics": metrics,
		"timelineSpec": map[string]any{
			"aggregationPeriod": period,
			"startTime":         toDateTime(start, hourly),
			"endTime":           toDateTime(end, hourly),
		},
	}
	if len(dimensions) > 0 {
		body["dimensions"] = dimensions
	}
	return json.Marshal(body)
}

func toDateTime(t time.Time, hourly bool) dateTime {
	d := dateTime{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
	if hourly {
		d.Hours = t.Hour()
	}
	return d
}

// warnFreshness always writes a one-line freshness notice to stderr so an empty
// window is never read as "zero". When data is present it reports the freshest
// datapoint in the window (dates are zero-padded, so the lexical max is the
// chronological max).
func warnFreshness(rc *kernel.RunContext, set vitals.MetricSet, tl vitals.Timeline) {
	if rc.Stderr == nil {
		return
	}
	if tl.Empty() {
		_, _ = fmt.Fprintf(rc.Stderr,
			"WARN: no %s datapoints in the requested window; vitals metrics are reported with a delay, so an empty window is not the same as zero.\n",
			set.Name)
		return
	}
	latest := ""
	for _, r := range tl.Rows {
		if r.Date > latest {
			latest = r.Date
		}
	}
	_, _ = fmt.Fprintf(rc.Stderr,
		"NOTE: %s vitals are reported with a delay; freshest datapoint in this window: %s.\n",
		set.Name, latest)
}

// NewCommand returns the cobra command for `gplay vitals query <metric-set>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "query <metric-set>",
		Short: "Query a Play vitals metric set (crashrate, anrrate, …) over a window",
		Long: `Query one of the Play Developer Reporting metric sets directly — the
generic, full-coverage form the opinionated presets (vitals crashes, vitals
anr, …) wrap.

  gplay vitals query crashrate --package com.example.app
  gplay vitals query crashrate --metrics crashRate,distinctUsers --dimensions versionCode
  gplay vitals query anrrate --period HOURLY --since 24h

--metrics and --dimensions are validated OFFLINE against the embedded API
schema; unknown values are rejected with the valid set listed (names are never
invented — they come from the snapshot). With no --metrics the set's primary
metric is used. Default window: --since 28d, --period DAILY (HOURLY opt-in).

This is a READ-ONLY surface on a distinct Google service (Play Developer
Reporting), requested with the least-privilege playdeveloperreporting scope.

--output json mirrors the API response verbatim; table/markdown render the
timeline (dates × metrics, sliced by --dimensions). A freshness note is always
printed to stderr so an empty window is not mistaken for zero.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.MetricSet = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringSliceVar(&in.Metrics, "metrics", nil, "metrics to aggregate (default: the set's primary metric); validated against the schema")
	cmd.Flags().StringSliceVar(&in.Dimensions, "dimensions", nil, "dimensions to slice by (e.g. versionCode,countryCode); validated against the schema")
	cmd.Flags().StringVar(&in.Period, "period", defaultPeriod, "aggregation period: DAILY, HOURLY, or FULL_RANGE")
	cmd.Flags().StringVar(&in.Since, "since", defaultSince, "window length back from now, e.g. 28d or 24h")
	return cmd
}
