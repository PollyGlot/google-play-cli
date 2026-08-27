package vitals_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

func TestActiveBetween_rfc3339Quoted(t *testing.T) {
	start := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	got := vitals.ActiveBetween(start, end)
	want := `activeBetween("2026-05-19T00:00:00Z", "2026-06-16T00:00:00Z")`
	if got != want {
		t.Errorf("ActiveBetween = %q, want %q", got, want)
	}
}

func TestListAnomalies_getAndParse(t *testing.T) {
	var gotURL, gotMethod string
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		return jsonResp(200, `{"anomalies":[{
			"name":"apps/com.example.app/anomalies/xyz",
			"metricSet":"apps/com.example.app/crashRateMetricSet",
			"dimensions":[{"dimension":"versionCode","int64Value":"123"}],
			"metric":{"metric":"crashRate","decimalValue":{"value":"0.08"}},
			"timelineSpec":{"startTime":{"year":2026,"month":6,"day":10},"endTime":{"year":2026,"month":6,"day":12}}
		}]}`), nil
	})}
	raw, _, err := vitals.ListAnomalies(context.Background(), hc, "com.example.app", vitals.AnomalyListOptions{
		Filter: vitals.ActiveBetween(time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)),
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("ListAnomalies: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.Contains(gotURL, "/apps/com.example.app/anomalies") {
		t.Errorf("URL = %q", gotURL)
	}
	if !strings.Contains(gotURL, "activeBetween") || !strings.Contains(gotURL, "pageSize=50") {
		t.Errorf("URL missing filter/pageSize: %q", gotURL)
	}

	anoms, err := vitals.ParseAnomalies(raw)
	if err != nil {
		t.Fatalf("ParseAnomalies: %v", err)
	}
	if len(anoms) != 1 {
		t.Fatalf("anomalies = %d", len(anoms))
	}
	a := anoms[0]
	if a.MetricSet != "crashRateMetricSet" {
		t.Errorf("metricSet = %q, want crashRateMetricSet (last segment)", a.MetricSet)
	}
	if a.Metric != "crashRate" || a.Value != "0.08" {
		t.Errorf("metric/value = %q/%q", a.Metric, a.Value)
	}
	if a.Dimensions != "versionCode=123" {
		t.Errorf("dimensions = %q", a.Dimensions)
	}
	if a.Period != "2026-06-10 → 2026-06-12" {
		t.Errorf("period = %q", a.Period)
	}
}

func TestListAnomalies_nonOKIsAPIError(t *testing.T) {
	hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResp(403, `{"error":{"message":"no"}}`), nil
	})}
	_, _, err := vitals.ListAnomalies(context.Background(), hc, "com.example.app", vitals.AnomalyListOptions{})
	var apiErr *api.Error
	if err == nil || !asAPIErr(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Fatalf("want *api.Error 403, got %v", err)
	}
}
