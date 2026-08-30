package vitals_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// seqRT serves a fixed sequence of page bodies in order, recording the
// pageToken seen on each request (query param for GET, body field for POST).
type seqRT struct {
	mu    sync.Mutex
	pages []string
	n     int
	seen  []string // pageToken observed per non-token request
}

func (r *seqRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := http.Header{"Content-Type": []string{"application/json"}}
	tok := req.URL.Query().Get("pageToken")
	if req.Method == http.MethodPost && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(b, &m)
		if v, ok := m["pageToken"]; ok {
			_ = json.Unmarshal(v, &tok)
		}
	}
	r.seen = append(r.seen, tok)
	body := "{}"
	if r.n < len(r.pages) {
		body = r.pages[r.n]
	}
	r.n++
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
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
	rt := &seqRT{pages: []string{
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":1}}],"nextPageToken":"P2"}`,
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":2}}]}`,
	}}
	set, _ := vitals.MetricSetByName("crashrate")
	raw, err := vitals.Query(context.Background(), &http.Client{Transport: rt}, set, "com.example.app", []byte(`{"metrics":["crashRate"]}`))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got := countRows(t, raw, "rows"); got != 2 {
		t.Errorf("merged rows = %d, want 2 (both pages)", got)
	}
	// Page 2 must have carried the continuation token from page 1.
	if len(rt.seen) != 2 || rt.seen[0] != "" || rt.seen[1] != "P2" {
		t.Errorf("pageTokens seen = %v, want [\"\" \"P2\"]", rt.seen)
	}
}

func TestSearchErrorIssues_paginatesToLimit(t *testing.T) {
	rt := &seqRT{pages: []string{
		`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
		`{"errorIssues":[{"type":"ANR"},{"type":"ANR"}],"nextPageToken":"P3"}`,
		`{"errorIssues":[{"type":"NON_FATAL"}]}`,
	}}
	raw, _, err := vitals.SearchErrorIssues(context.Background(), &http.Client{Transport: rt}, "com.example.app", vitals.SearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("SearchErrorIssues: %v", err)
	}
	if got := countRows(t, raw, "errorIssues"); got != 3 {
		t.Errorf("issues = %d, want 3 (capped by --limit across pages)", got)
	}
	// It should have stopped after the 2nd page (2+2 >= 3), not fetched page 3.
	if rt.n != 2 {
		t.Errorf("fetched %d pages, want 2 (stop once limit reached)", rt.n)
	}
}

func TestListAnomalies_followsTokenToExhaustion(t *testing.T) {
	rt := &seqRT{pages: []string{
		`{"anomalies":[{"metricSet":"apps/x/crashRateMetricSet"}],"nextPageToken":"P2"}`,
		`{"anomalies":[{"metricSet":"apps/x/anrRateMetricSet"}]}`,
	}}
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
	rt := &seqRT{pages: []string{
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":1}}],"nextPageToken":"SAME"}`,
		`{"rows":[{"startTime":{"year":2026,"month":6,"day":2}}],"nextPageToken":"SAME"}`,
	}}
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
