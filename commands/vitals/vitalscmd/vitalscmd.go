package vitalscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

const (
	DefaultSince  = "28d"
	DefaultPeriod = "DAILY"
)

type Params struct {
	Set        vitals.MetricSet
	Package    string
	Metrics    []string
	Dimensions []string
	Period     string
	Since      string
	Filter     string
	UserCohort string
}

type Payload struct {
	Raw        json.RawMessage
	Timeline   vitals.Timeline
	Dimensions []string
	Metrics    []string
}

func (p Payload) Renderers() output.Renderers {
	cols := p.columns()
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, cols, p.Timeline.Rows) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, cols, p.Timeline.Rows) },
	}
}

func (p Payload) columns() []output.Column[vitals.Row] {
	cols := []output.Column[vitals.Row]{
		{Key: "date", Header: "DATE", Value: func(r vitals.Row) string { return r.Date }},
	}
	for _, d := range p.Dimensions {
		cols = append(cols, output.Column[vitals.Row]{
			Key: d, Header: strings.ToUpper(d), Value: func(r vitals.Row) string { return r.Dimensions[d] },
		})
	}
	for _, m := range p.Metrics {
		cols = append(cols, output.Column[vitals.Row]{
			Key: m, Header: m, Value: func(r vitals.Row) string { return r.Metrics[m] },
		})
	}
	return cols
}

func Execute(rc *kernel.RunContext, p Params) (output.Renderable, error) {
	pkg, err := rc.Package(p.Package)
	if err != nil {
		return nil, err
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
	if err := validateAll("period", []string{period}, vitals.SupportedPeriods(idx, p.Set), p.Set.Name); err != nil {
		return nil, err
	}

	metrics := p.Metrics
	supportedMetrics := vitals.SupportedMetrics(idx, p.Set)
	if len(metrics) == 0 {
		if len(supportedMetrics) == 0 {
			return nil, fmt.Errorf("metric set %q has no metrics in the index", p.Set.Name)
		}
		metrics = []string{supportedMetrics[0]}
	}
	if err := validateAll("metric", metrics, supportedMetrics, p.Set.Name); err != nil {
		return nil, err
	}

	cohort := strings.ToUpper(p.UserCohort)
	dimensionScope, supportedDimensions := p.Set.Name, vitals.SupportedDimensions(idx, p.Set)
	if cohort != "" {
		if err := validateAll("user cohort", []string{cohort}, vitals.SupportedCohorts(idx, p.Set), p.Set.Name); err != nil {
			return nil, err
		}
		if narrowed := vitals.CohortDimensions(idx, p.Set, cohort); narrowed != nil {
			dimensionScope, supportedDimensions = p.Set.Name+" with user cohort "+cohort, narrowed
		}
	}
	dimensions := p.Dimensions
	if err := validateAll("dimension", dimensions, supportedDimensions, dimensionScope); err != nil {
		return nil, err
	}

	since, err := ParseSince(p.Since)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	latest, err := LatestEnd(rc, httpClient, p.Set, pkg, period)
	if err != nil {
		return nil, err
	}

	body, err := buildBody(metrics, dimensions, period, p.Filter, cohort, since, time.Now().UTC(), latest)
	if err != nil {
		return nil, err
	}

	raw, err := vitals.Query(rc.Ctx, httpClient, p.Set, pkg, body)
	if err != nil {
		return nil, err
	}

	tl, err := vitals.ParseTimeline(pkg, raw)
	if err != nil {
		return nil, err
	}

	warnFreshness(rc, p.Set, tl)

	return Payload{Raw: raw, Timeline: tl, Dimensions: dimensions, Metrics: metrics}, nil
}

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

type dateTime struct {
	Year  int  `json:"year"`
	Month int  `json:"month"`
	Day   int  `json:"day"`
	Hours *int `json:"hours,omitempty"`
}

func LatestEnd(rc *kernel.RunContext, hc *http.Client, set vitals.MetricSet, pkg, period string) (time.Time, error) {
	raw, err := vitals.Describe(rc.Ctx, hc, set, pkg)
	if err != nil {
		return time.Time{}, err
	}
	fresh, err := vitals.ParseFreshness(pkg, raw)
	if err != nil {
		return time.Time{}, err
	}
	for _, f := range fresh {
		if f.Period == period {
			return f.LatestEnd, nil
		}
	}
	return time.Time{}, nil
}

func buildBody(metrics, dimensions []string, period, filter, cohort string, since time.Duration, now, latest time.Time) ([]byte, error) {
	hourly := period == "HOURLY"
	var end, start time.Time
	if hourly {
		end = now.Truncate(time.Hour)
	} else {
		end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	if !latest.IsZero() && latest.Before(end) {
		end = latest
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
	if cohort != "" {
		body["userCohort"] = cohort
	}
	return output.Marshal(body)
}

func toDateTime(t time.Time, hourly bool) dateTime {
	d := dateTime{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}
	if hourly {
		h := t.Hour()
		d.Hours = &h
	}
	return d
}

func warnFreshness(rc *kernel.RunContext, set vitals.MetricSet, tl vitals.Timeline) {
	if tl.Empty() {
		rc.Warnf("no %s datapoints in the requested window; vitals metrics are reported with a delay, so an empty window is not the same as zero.",
			set.Name)
		return
	}
	latest := ""
	for _, r := range tl.Rows {
		if r.Date > latest {
			latest = r.Date
		}
	}
	rc.Notef("%s vitals are reported with a delay; freshest datapoint in this window: %s.",
		set.Name, latest)
}
