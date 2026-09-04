package vitals_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestMetricSetByName resolves the gplay-facing metric-set name to its REST
// resource segment, and rejects an unknown set.
func TestMetricSetByName(t *testing.T) {
	set, ok := vitals.MetricSetByName("crashrate")
	if !ok {
		t.Fatal("crashrate not in registry")
	}
	if set.Resource != "crashRateMetricSet" {
		t.Errorf("resource = %q, want crashRateMetricSet", set.Resource)
	}
	if got := set.QueryMethodID(); got != "playdeveloperreporting.vitals.crashrate.query" {
		t.Errorf("QueryMethodID = %q", got)
	}
	if _, ok := vitals.MetricSetByName("nonsense"); ok {
		t.Error("unknown metric set resolved")
	}
}

// TestQuery_issuesPOST asserts Query POSTs the body to the metric set's
// :query path under the reporting base and returns the verbatim 2xx body.
func TestQuery_issuesPOST(t *testing.T) {
	set, _ := vitals.MetricSetByName("crashrate")
	var gotURL, gotMethod, gotBody, gotCT string
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return jsonResp(200, `{"rows":[]}`), nil
	})}

	raw, err := vitals.Query(context.Background(), hc, set, "com.example.app", []byte(`{"metrics":["crashRate"]}`))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	wantURL := "https://playdeveloperreporting.googleapis.com/v1beta1/apps/com.example.app/crashRateMetricSet:query"
	if gotURL != wantURL {
		t.Errorf("URL = %q, want %q", gotURL, wantURL)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody != `{"metrics":["crashRate"]}` {
		t.Errorf("body = %q", gotBody)
	}
	if string(raw) != `{"rows":[]}` {
		t.Errorf("raw = %q", raw)
	}
}

// TestQuery_nonOKBecomesAPIError maps a 403 to an *api.Error so the shared
// classifier surfaces it as exit 11.
func TestQuery_nonOKBecomesAPIError(t *testing.T) {
	set, _ := vitals.MetricSetByName("crashrate")
	hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResp(403, `{"error":{"message":"denied"}}`), nil
	})}
	_, err := vitals.Query(context.Background(), hc, set, "com.example.app", []byte(`{}`))
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *api.Error, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("status = %d, want 403", apiErr.StatusCode)
	}
}

// TestSupportedMetricsAndDimensions reads the catalog straight from the
// embedded index (never invented): the crashrate request schema documents
// `crashRate` (metric) and `versionCode` (dimension) in its property prose.
func TestSupportedMetricsAndDimensions(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	set, _ := vitals.MetricSetByName("crashrate")

	metrics := vitals.SupportedMetrics(idx, set)
	if !contains(metrics, "crashRate") {
		t.Errorf("supported metrics %v missing crashRate", metrics)
	}
	if !contains(metrics, "distinctUsers") {
		t.Errorf("supported metrics %v missing distinctUsers", metrics)
	}
	dims := vitals.SupportedDimensions(idx, set)
	if !contains(dims, "versionCode") || !contains(dims, "deviceModel") || !contains(dims, "countryCode") {
		t.Errorf("supported dimensions %v missing expected entries", dims)
	}

	periods := vitals.SupportedPeriods(idx, set)
	if !contains(periods, "DAILY") || !contains(periods, "HOURLY") {
		t.Errorf("supported periods %v missing DAILY/HOURLY", periods)
	}
	// The UNSPECIFIED sentinel is never a user-selectable value.
	if contains(periods, "AGGREGATION_PERIOD_UNSPECIFIED") {
		t.Errorf("periods %v leaked the UNSPECIFIED sentinel", periods)
	}
}

// TestSupportedPeriods_memorySetsAreDailyOnly reads the per-set aggregation
// periods from the snapshot: the memory sets document DAILY alone, so HOURLY
// must not be offered for them (#440).
func TestSupportedPeriods_memorySetsAreDailyOnly(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, name := range []string{"anonrssandswapmemoryusage", "bitmapmemoryusage"} {
		set, ok := vitals.MetricSetByName(name)
		if !ok {
			t.Fatalf("metric set %q not registered", name)
		}
		periods := vitals.SupportedPeriods(idx, set)
		if len(periods) != 1 || periods[0] != "DAILY" {
			t.Errorf("%s: supported periods = %v, want [DAILY]", name, periods)
		}
	}
}

// TestSupportedMetrics_memorySets proves the memory sets expose their
// percentile catalog, read verbatim from the embedded index (#440).
func TestSupportedMetrics_memorySets(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	cases := map[string]string{
		"anonrssandswapmemoryusage": "anonRssAndSwapMemoryUsageP99",
		"bitmapmemoryusage":         "bitmapMemoryUsageP99",
	}
	for name, want := range cases {
		set, ok := vitals.MetricSetByName(name)
		if !ok {
			t.Fatalf("metric set %q not registered", name)
		}
		metrics := vitals.SupportedMetrics(idx, set)
		if !contains(metrics, want) || !contains(metrics, "distinctUsers") {
			t.Errorf("%s: supported metrics %v missing %s/distinctUsers", name, metrics, want)
		}
		if !contains(vitals.SupportedDimensions(idx, set), "deviceRamBucket") {
			t.Errorf("%s: supported dimensions missing deviceRamBucket", name)
		}
	}
}

// TestParseTimeline turns a query response into dated rows with dimension and
// metric columns keyed by name.
func TestParseTimeline(t *testing.T) {
	body := []byte(`{"rows":[
	  {"startTime":{"year":2026,"month":6,"day":1},
	   "dimensions":[{"dimension":"versionCode","int64Value":"123"}],
	   "metrics":[{"metric":"crashRate","decimalValue":{"value":"0.0123"}}]},
	  {"startTime":{"year":2026,"month":6,"day":2},
	   "dimensions":[{"dimension":"versionCode","int64Value":"123"}],
	   "metrics":[{"metric":"crashRate","decimalValue":{"value":"0.0156"}}]}
	]}`)
	tl, err := vitals.ParseTimeline(body)
	if err != nil {
		t.Fatalf("ParseTimeline: %v", err)
	}
	if len(tl.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tl.Rows))
	}
	r0 := tl.Rows[0]
	if r0.Date != "2026-06-01" {
		t.Errorf("date = %q, want 2026-06-01", r0.Date)
	}
	if r0.Dimensions["versionCode"] != "123" {
		t.Errorf("versionCode = %q, want 123", r0.Dimensions["versionCode"])
	}
	if r0.Metrics["crashRate"] != "0.0123" {
		t.Errorf("crashRate = %q, want 0.0123", r0.Metrics["crashRate"])
	}
	if tl.Empty() {
		t.Error("timeline should not be empty")
	}
}

// TestParseTimeline_hourlyMidnightCarriesTime asserts the 00:00 bucket of an
// HOURLY timeline renders with the time suffix, consistent with every other
// hour (the row's aggregationPeriod drives it).
func TestParseTimeline_hourlyMidnightCarriesTime(t *testing.T) {
	body := []byte(`{"rows":[
	  {"startTime":{"year":2026,"month":6,"day":1,"hours":0},"aggregationPeriod":"HOURLY","metrics":[{"metric":"crashRate","decimalValue":{"value":"0.01"}}]},
	  {"startTime":{"year":2026,"month":6,"day":1,"hours":13},"aggregationPeriod":"HOURLY","metrics":[{"metric":"crashRate","decimalValue":{"value":"0.02"}}]}
	]}`)
	tl, err := vitals.ParseTimeline(body)
	if err != nil {
		t.Fatalf("ParseTimeline: %v", err)
	}
	if tl.Rows[0].Date != "2026-06-01 00:00" {
		t.Errorf("midnight HOURLY date = %q, want 2026-06-01 00:00", tl.Rows[0].Date)
	}
	if tl.Rows[1].Date != "2026-06-01 13:00" {
		t.Errorf("13:00 HOURLY date = %q, want 2026-06-01 13:00", tl.Rows[1].Date)
	}
}

// TestQuery_emptyBodyTolerated asserts an empty 2xx page body does not surface
// as a decode error: it yields an empty {rows:[]} envelope.
func TestQuery_emptyBodyTolerated(t *testing.T) {
	set, _ := vitals.MetricSetByName("crashrate")
	hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	raw, err := vitals.Query(context.Background(), hc, set, "com.example.app", []byte(`{"metrics":["crashRate"]}`))
	if err != nil {
		t.Fatalf("Query on empty body: %v", err)
	}
	tl, err := vitals.ParseTimeline(raw)
	if err != nil || !tl.Empty() {
		t.Errorf("empty body should yield an empty timeline, got %d rows / err %v", len(tl.Rows), err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
