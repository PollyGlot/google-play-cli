package vitals_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	set, ok := vitals.MetricSetByName("crashrate")
	if !ok {
		t.Fatal("crashrate metric set missing")
	}
	query := func(hc *http.Client) (any, error) {
		v, err := vitals.Query(ctx, hc, set, pkg, []byte(`{"metrics":["crashRate"],"timelineSpec":{"aggregationPeriod":"DAILY"},"pageToken":"stale"}`))
		return v, err
	}
	anomalies := func(limit int) func(*http.Client) (any, error) {
		return func(hc *http.Client) (any, error) {
			v, raw, err := vitals.ListAnomalies(ctx, hc, pkg, vitals.AnomalyListOptions{Filter: `activeBetween("a", "b")`, Limit: limit})
			return []any{v, raw}, err
		}
	}
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	issues := func(limit int) func(*http.Client) (any, error) {
		return func(hc *http.Client) (any, error) {
			v, raw, err := vitals.SearchErrorIssues(ctx, hc, pkg, vitals.SearchOptions{Filter: "errorIssueType = CRASH", OrderBy: "errorReportCount desc", Start: day, End: day.AddDate(0, 0, 7), Limit: limit})
			return []any{v, raw}, err
		}
	}
	reports := func(hc *http.Client) (any, error) {
		v, raw, err := vitals.SearchErrorReports(ctx, hc, pkg, vitals.SearchOptions{})
		return []any{v, raw}, err
	}
	sends := []testkit.Send{
		{Name: "Query", Fn: query},
		{Name: "Describe", Fn: func(hc *http.Client) (any, error) { v, err := vitals.Describe(ctx, hc, set, pkg); return v, err }},
		{Name: "ListAnomalies", Fn: anomalies(0)},
		{Name: "SearchErrorIssues", Fn: issues(0)},
		{Name: "SearchErrorReports", Fn: reports},
	}
	var b strings.Builder
	b.WriteString(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))

	pages := func(key string) func(string, int, string) string {
		return func(first string, n int, next string) string {
			items := make([]string, n)
			for i := range items {
				items[i] = `{"name":"` + first + string(rune('a'+i)) + `"}`
			}
			return `{"` + key + `":[` + strings.Join(items, ",") + `],"nextPageToken":"` + next + `"}`
		}
	}
	for _, l := range []struct {
		name string
		fn   func(*http.Client) (any, error)
		page func(string, int, string) string
	}{
		{"Query", query, pages("rows")},
		{"ListAnomalies", anomalies(0), pages("anomalies")},
		{"ListAnomalies limit 3", anomalies(3), pages("anomalies")},
		{"SearchErrorIssues limit 2", issues(2), pages("errorIssues")},
		{"SearchErrorReports", reports, pages("errorReports")},
	} {
		one := []testkit.Send{{Name: l.name, Fn: l.fn}}
		b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(l.page("p1", 2, "p/2+"), l.page("p2", 2, ""))}}))
		b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(l.page("p1", 1, "again"))}}))
		b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "empty body", Responders: []testkit.Responder{testkit.Any(http.StatusOK, "")}}))
	}
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
