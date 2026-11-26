package vitals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opListAnomalies  = "playdeveloperreporting.anomalies.list"
	pageMaxAnomalies = 100
)

var mListAnomalies = apiregistry.MustResolve("playdeveloperreporting.anomalies.list")

type AnomalyListOptions struct {
	Filter string
	Limit  int
}

func ActiveBetween(start, end time.Time) string {
	return fmt.Sprintf("activeBetween(%q, %q)",
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
}

func ListAnomalies(ctx context.Context, hc *http.Client, pkg string, opts AnomalyListOptions) (json.RawMessage, bool, error) {
	base := url.Values{}
	if opts.Filter != "" {
		base.Set("filter", opts.Filter)
	}
	return paginateGET(ctx, hc, mListAnomalies, opListAnomalies, pkg, base, "anomalies", pageMaxAnomalies, opts.Limit)
}

type Anomaly struct {
	MetricSet  string
	Metric     string
	Value      string
	Dimensions string
	Period     string
}

func ParseAnomalies(pkg string, body []byte) ([]Anomaly, error) {
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
		return nil, api.DecodeFailure(api.Error{Operation: opListAnomalies, Package: pkg, Message: "decode anomalies: " + err.Error(), Cause: err})
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

func lastPathSegment(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func period(start, end apiDateTime) string {
	s := start.format(false)
	e := end.format(false)
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
