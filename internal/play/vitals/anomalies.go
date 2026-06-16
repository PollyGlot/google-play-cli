package vitals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opListAnomalies = "playdeveloperreporting.anomalies.list"

// AnomalyListOptions bounds an anomalies.list call: an AIP-160 Filter (the
// window is expressed as an `activeBetween(...)` filter — see ActiveBetween) and
// a PageSize.
type AnomalyListOptions struct {
	Filter   string
	PageSize int
}

// ActiveBetween builds the anomalies `activeBetween("start", "end")` filter from
// a window, the documented way to bound anomalies.list by time (the method has
// no interval parameters). Timestamps are RFC-3339 in UTC.
func ActiveBetween(start, end time.Time) string {
	return fmt.Sprintf("activeBetween(%q, %q)",
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
}

// ListAnomalies issues anomalies.list (GET) for pkg and returns the verbatim 2xx
// body (Play-detected metric anomalies) for the JSON pass-through.
func ListAnomalies(ctx context.Context, hc *http.Client, pkg string, opts AnomalyListOptions) (json.RawMessage, error) {
	u := api.ReportingBase + "/apps/" + url.PathEscape(pkg) + "/anomalies"
	q := url.Values{}
	if opts.Filter != "" {
		q.Set("filter", opts.Filter)
	}
	if opts.PageSize > 0 {
		q.Set("pageSize", strconv.Itoa(opts.PageSize))
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &api.Error{Operation: opListAnomalies, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opListAnomalies, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(errBody, resp.StatusCode)
		return nil, &api.Error{Operation: opListAnomalies, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: opListAnomalies, Package: pkg, StatusCode: resp.StatusCode, Message: "read response: " + readErr.Error(), Cause: readErr}
	}
	return raw, nil
}

// Anomaly is the render-ready subset of a Play-detected metric anomaly: which
// metric of which metric set spiked, the anomalous value, the period it covers,
// and the dimensions it is sliced by.
type Anomaly struct {
	MetricSet  string
	Metric     string
	Value      string
	Dimensions string
	Period     string
}

// ParseAnomalies projects an anomalies.list response into Anomalies.
func ParseAnomalies(body []byte) ([]Anomaly, error) {
	var resp struct {
		Anomalies []struct {
			MetricSet    string              `json:"metricSet"`
			Dimensions   []apiDimensionValue `json:"dimensions"`
			Metric       apiMetricValue      `json:"metric"`
			TimelineSpec struct {
				StartTime apiDateTime `json:"startTime"`
				EndTime   apiDateTime `json:"endTime"`
			} `json:"timelineSpec"`
		} `json:"anomalies"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode anomalies: %w", err)
	}
	out := make([]Anomaly, 0, len(resp.Anomalies))
	for _, a := range resp.Anomalies {
		dims := make([]string, 0, len(a.Dimensions))
		for _, d := range a.Dimensions {
			dims = append(dims, d.Dimension+"="+d.value())
		}
		out = append(out, Anomaly{
			MetricSet:  lastPathSegment(a.MetricSet),
			Metric:     a.Metric.Metric,
			Value:      a.Metric.DecimalValue.Value,
			Dimensions: strings.Join(dims, ", "),
			Period:     period(a.TimelineSpec.StartTime, a.TimelineSpec.EndTime),
		})
	}
	return out, nil
}

// lastPathSegment trims a resource name like "apps/X/crashRateMetricSet" to its
// final segment ("crashRateMetricSet"), the part a reader recognises.
func lastPathSegment(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// period renders the anomaly's [start, end) window compactly; an unset endpoint
// is omitted.
func period(start, end apiDateTime) string {
	s := start.format()
	e := end.format()
	switch {
	case s != "" && e != "":
		return s + " → " + e
	case s != "":
		return s + " →"
	case e != "":
		return "→ " + e
	default:
		return ""
	}
}
