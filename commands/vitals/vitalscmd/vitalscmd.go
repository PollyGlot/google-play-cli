// Package vitalscmd is the shared orchestration behind every `gplay vitals`
// metric-set command: the generic `vitals query <metric-set>` escape hatch and
// the opinionated presets (`vitals crashes`, `vitals anr`, …). A preset is just
// a Params with a fixed metric set and a friendlier flag surface; both route
// through Execute, so validation, request shaping, the `:query` call, the
// timeline projection, the JSON pass-through and the freshness warning live in
// one place (#49).
package vitalscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// Defaults mirror the Play Console default view (#49).
const (
	DefaultSince  = "28d"
	DefaultPeriod = "DAILY"
)

// Params is the resolved request a vitals metric-set command runs. Metrics
// empty → the set's primary metric; Dimensions empty → one row per period;
// Period/Since empty → the defaults; Filter empty → no row filter.
type Params struct {
	Set        vitals.MetricSet
	Package    string
	Metrics    []string
	Dimensions []string
	Period     string
	Since      string
	Filter     string // AIP-160 predicate, e.g. "versionCode = 123"
}

// Payload is the rendered result: the verbatim API response for the JSON
// pass-through (ADR-0003), plus the parsed Timeline and the resolved column
// order (dimensions then metrics) for table/markdown.
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

// Execute validates p against the embedded Schema index, issues the `:query`
// POST, projects the timeline, warns about freshness, and returns the Payload.
// It is the single body both the generic command and the presets call.
func Execute(rc *kernel.RunContext, p Params) (output.Renderable, error) {
	pkg := p.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, exit.Usagef("no package: pass --package <pkg> or run gplay init in your repo")
	}

	idx, err := schemaindex.Embedded()
	if err != nil {
		return nil, fmt.Errorf("load embedded schema index: %w", err)
	}

	period := p.Period
	if period == "" {
		period = DefaultPeriod
	}
	period = strings.ToUpper(period)
	// Periods are per set: the memory sets are DAILY-only, so naming the set in
	// the rejection is what makes the error actionable (#440).
	if err := validateAll("period", []string{period}, vitals.SupportedPeriods(idx, p.Set), p.Set.Name); err != nil {
		return nil, err
	}

	metrics := p.Metrics
	supportedMetrics := vitals.SupportedMetrics(idx, p.Set)
	if len(metrics) == 0 {
		if len(supportedMetrics) == 0 {
			return nil, fmt.Errorf("metric set %q has no metrics in the index", p.Set.Name)
		}
		metrics = []string{supportedMetrics[0]} // the set's primary metric
	}
	if err := validateAll("metric", metrics, supportedMetrics, p.Set.Name); err != nil {
		return nil, err
	}

	dimensions := p.Dimensions
	if err := validateAll("dimension", dimensions, vitals.SupportedDimensions(idx, p.Set), p.Set.Name); err != nil {
		return nil, err
	}

	since, err := ParseSince(p.Since)
	if err != nil {
		return nil, err
	}

	body, err := buildBody(metrics, dimensions, period, p.Filter, since, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	raw, err := vitals.Query(rc.Ctx, httpClient, p.Set, pkg, body)
	if err != nil {
		return nil, err
	}

	tl, err := vitals.ParseTimeline(raw)
	if err != nil {
		return nil, err
	}

	warnFreshness(rc, p.Set, tl)

	return Payload{Raw: raw, Timeline: tl, Dimensions: dimensions, Metrics: metrics}, nil
}

// validateAll rejects any value not present in allowed, naming the metric set.
// allowed comes straight from the snapshot.
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

// ParseSince turns a window spec into a duration: "Nd" (days) or any Go
// duration ("24h", "90m"); empty defaults to 28d. A non-positive or malformed
// value is CLI misuse.
func ParseSince(spec string) (time.Duration, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		s = DefaultSince
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
// HOURLY): see the TimelineSpec prose in the snapshot.
//
// Hours is a *int, not an int: an HOURLY window whose boundary lands on
// midnight (hour 0) must still carry `"hours":0`. With a bare int + omitempty,
// hour 0 would be dropped, sending a date-only datetime for an HOURLY request:
// indistinguishable from the DAILY (date-only) shape. The pointer omits only
// the DAILY case (nil) and keeps an explicit 0 for HOURLY.
type dateTime struct {
	Year  int  `json:"year"`
	Month int  `json:"month"`
	Day   int  `json:"day"`
	Hours *int `json:"hours,omitempty"`
}

// buildBody assembles the `:query` request body for the given window. endTime is
// the period boundary at/after now (exclusive); startTime is endTime minus the
// window. HOURLY carries the hour field; DAILY/FULL_RANGE are date-only.
func buildBody(metrics, dimensions []string, period, filter string, since time.Duration, now time.Time) ([]byte, error) {
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
	if filter != "" {
		body["filter"] = filter
	}
	return json.Marshal(body)
}

func toDateTime(t time.Time, hourly bool) dateTime {
	d := dateTime{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
	if hourly {
		h := t.Hour()
		d.Hours = &h
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
