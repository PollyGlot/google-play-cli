package vitals_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// seqFake serves a fixed sequence of page bodies in order, then {} once they
// run out.
func seqFake(pages ...string) *testkit.Fake {
	var (
		mu sync.Mutex
		n  int
	)
	return testkit.NewFake(func(testkit.Call) (int, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		body := "{}"
		if n < len(pages) {
			body = pages[n]
		}
		n++
		return http.StatusOK, body, true
	})
}

// pageTokens returns the pageToken each request carried (query param for GET,
// body field for POST).
func pageTokens(fake *testkit.Fake) []string {
	var seen []string
	for _, c := range fake.Calls() {
		q, _ := url.ParseQuery(c.Query)
		tok := q.Get("pageToken")
		if c.Method == http.MethodPost && c.Body != nil {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(c.Body, &m)
			if v, ok := m["pageToken"]; ok {
				_ = json.Unmarshal(v, &tok)
			}
		}
		seen = append(seen, tok)
	}
	return seen
}

func countRows(t *testing.T, raw json.RawMessage, key string) int {
	t.Helper()
	var m map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal rebuilt envelope: %v", err)
	}
	return len(m[key])
}

func TestQuery_followsNextPageToken(t *testing.T) {
	rt := seqFake(
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":1}}],"nextPageToken":"P2"}`,
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":2}}]}`,
	)
	set, _ := vitals.MetricSetByName("crashrate")
	raw, err := vitals.Query(context.Background(), &http.Client{Transport: rt}, set, "com.example.app", []byte(`{"metrics":["crashRate"]}`))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := countRows(t, raw, "rows"); got != 2 {
		t.Errorf("merged rows = %d, want 2 (both pages)", got)
	}
	// Page 2 must have carried the continuation token from page 1.
	if seen := pageTokens(rt); len(seen) != 2 || seen[0] != "" || seen[1] != "P2" {
		t.Errorf("pageTokens seen = %v, want [\"\" \"P2\"]", seen)
	}
}

func TestSearchErrorIssues_paginatesToLimit(t *testing.T) {
	rt := seqFake(
		`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
		`{"errorIssues":[{"type":"ANR"},{"type":"ANR"}],"nextPageToken":"P3"}`,
		`{"errorIssues":[{"type":"NON_FATAL"}]}`,
	)
	raw, _, err := vitals.SearchErrorIssues(context.Background(), &http.Client{Transport: rt}, "com.example.app", vitals.SearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("SearchErrorIssues: %v", err)
	}
	if got := countRows(t, raw, "errorIssues"); got != 3 {
		t.Errorf("issues = %d, want 3 (capped by --limit across pages)", got)
	}
	// It should have stopped after the 2nd page (2+2 >= 3), not fetched page 3.
	if n := len(rt.Calls()); n != 2 {
		t.Errorf("fetched %d pages, want 2 (stop once limit reached)", n)
	}
}

func TestListAnomalies_followsTokenToExhaustion(t *testing.T) {
	rt := seqFake(
		`{"anomalies":[{"metricSet":"apps/x/crashRateMetricSet"}],"nextPageToken":"P2"}`,
		`{"anomalies":[{"metricSet":"apps/x/anrRateMetricSet"}]}`,
	)
	raw, _, err := vitals.ListAnomalies(context.Background(), &http.Client{Transport: rt}, "com.example.app", vitals.AnomalyListOptions{})
	if err != nil {
		t.Fatalf("ListAnomalies: %v", err)
	}
	if got := countRows(t, raw, "anomalies"); got != 2 {
		t.Errorf("anomalies = %d, want 2 (both pages, no cap)", got)
	}
}

func TestQuery_tokenLoopDetected(t *testing.T) {
	// A server that repeats the same nextPageToken must fail loudly, not loop.
	rt := seqFake(
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":1}}],"nextPageToken":"SAME"}`,
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":2}}],"nextPageToken":"SAME"}`,
	)
	set, _ := vitals.MetricSetByName("crashrate")
	_, err := vitals.Query(context.Background(), &http.Client{Transport: rt}, set, "com.example.app", []byte(`{}`))
	var apiErr *api.Error
	if err == nil || !asAPIErr(err, &apiErr) {
		t.Fatalf("want *api.Error on token loop, got %v", err)
	}
	if !strings.Contains(apiErr.Message, "loop") {
		t.Errorf("error = %q, want a loop-detection message", apiErr.Message)
	}
}
