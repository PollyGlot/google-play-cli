package vitals_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

const crashrateDescriptor = `{
  "name": "apps/com.example.app/crashRateMetricSet",
  "freshnessInfo": {"freshnesses": [
    {"aggregationPeriod": "DAILY", "latestEndTime": {"year": 2026, "month": 9, "day": 12, "timeZone": {"id": "America/Los_Angeles"}}},
    {"aggregationPeriod": "HOURLY", "latestEndTime": {"year": 2026, "month": 9, "day": 13, "hours": 6, "timeZone": {"id": "UTC"}}}
  ]}
}`

// TestDescribe_getsDescriptorAndParsesFreshness pins the `.get` call shape: a
// GET (no body) on the metric set resource itself (no `:query` suffix), and a
// freshness projection that keeps the API's period order, renders DAILY
// date-only and HOURLY with the hour, and carries the timezone id (#545).
func TestDescribe_getsDescriptorAndParsesFreshness(t *testing.T) {
	var gotURL, gotMethod string
	var gotBody []byte
	hc := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		return jsonResp(200, crashrateDescriptor), nil
	})}
	set, ok := vitals.MetricSetByName("crashrate")
	if !ok {
		t.Fatal("crashrate not in registry")
	}
	raw, err := vitals.Describe(context.Background(), hc, set, "com.example.app")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/apps/com.example.app/crashRateMetricSet") {
		t.Errorf("URL = %q, want the bare metric set resource", gotURL)
	}
	if strings.Contains(gotURL, ":query") {
		t.Errorf("describe must not hit :query: %q", gotURL)
	}
	if len(gotBody) != 0 {
		t.Errorf("GET carried a body: %s", gotBody)
	}
	// Verbatim pass-through: the descriptor name survives untouched.
	if !strings.Contains(string(raw), `"apps/com.example.app/crashRateMetricSet"`) {
		t.Errorf("raw body altered: %s", raw)
	}

	fresh, err := vitals.ParseFreshness(raw)
	if err != nil {
		t.Fatalf("ParseFreshness: %v", err)
	}
	if len(fresh) != 2 {
		t.Fatalf("freshnesses = %d, want 2", len(fresh))
	}
	if fresh[0] != (vitals.Freshness{Period: "DAILY", LatestEndTime: "2026-09-12", TimeZone: "America/Los_Angeles"}) {
		t.Errorf("DAILY = %+v", fresh[0])
	}
	if fresh[1] != (vitals.Freshness{Period: "HOURLY", LatestEndTime: "2026-09-13 06:00", TimeZone: "UTC"}) {
		t.Errorf("HOURLY = %+v", fresh[1])
	}
}

func TestDescribe_nonOKIsAPIError(t *testing.T) {
	hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResp(403, `{"error":{"message":"no"}}`), nil
	})}
	set, _ := vitals.MetricSetByName("anrrate")
	_, err := vitals.Describe(context.Background(), hc, set, "com.example.app")
	var apiErr *api.Error
	if err == nil || !asAPIErr(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Fatalf("want *api.Error 403, got %v", err)
	}
}

// TestGetMethodIDs_anchoredToSnapshot extends the registry integrity gate to
// the `.get` side: every declared set (and the errors.counts set reached from
// `vitals errors counts`) must have its descriptor method in the embedded
// index, as a GET on the bare resource. Ten methods in all (#545).
func TestGetMethodIDs_anchoredToSnapshot(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	sets := append(vitals.MetricSets(), vitals.ErrorCountSet())
	if len(sets) != 10 {
		t.Fatalf("metric sets with a .get = %d, want 10", len(sets))
	}
	for _, set := range sets {
		m, ok := idx.Methods[set.GetMethodID()]
		if !ok {
			t.Errorf("metric set %q: missing get method %q in index", set.Name, set.GetMethodID())
			continue
		}
		if m.HTTPMethod != http.MethodGet {
			t.Errorf("metric set %q: %s verb = %q, want GET", set.Name, set.GetMethodID(), m.HTTPMethod)
		}
		if !strings.HasSuffix(m.Path, "/"+set.Resource) {
			t.Errorf("metric set %q: index path %q does not end with resource %q", set.Name, m.Path, set.Resource)
		}
	}
}
