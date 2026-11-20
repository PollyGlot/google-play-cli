package vitals_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

func TestSearchErrorIssues_truncationSignal(t *testing.T) {
	cases := []struct {
		name  string
		pages []string
		limit int
		want  bool
	}{
		{
			name: "limit-cuts-while-server-has-more",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
				`{"errorIssues":[{"type":"ANR"},{"type":"ANR"}],"nextPageToken":"P3"}`,
				`{"errorIssues":[{"type":"NON_FATAL"}]}`,
			},
			limit: 3,
			want:  true,
		},
		{
			name: "single-page-overshoots-the-limit",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"},{"type":"ANR"},{"type":"ANR"},{"type":"NON_FATAL"}]}`,
			},
			limit: 3,
			want:  true,
		},
		{
			name: "later-page-overshoots-the-limit",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"}],"nextPageToken":"P2"}`,
				`{"errorIssues":[{"type":"ANR"},{"type":"ANR"},{"type":"ANR"}]}`,
			},
			limit: 2,
			want:  true,
		},
		{
			name: "limit-reached-exactly-on-last-page",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
				`{"errorIssues":[{"type":"ANR"}]}`,
			},
			limit: 3,
			want:  false,
		},
		{
			name: "no-limit-exhausts-the-token",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"}],"nextPageToken":"P2"}`,
				`{"errorIssues":[{"type":"ANR"}]}`,
			},
			limit: 0,
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := seqFake(tc.pages...)
			raw, truncated, err := vitals.SearchErrorIssues(context.Background(),
				&http.Client{Transport: rt}, "com.example.app", vitals.SearchOptions{Limit: tc.limit})
			if err != nil {
				t.Fatalf("SearchErrorIssues: %v", err)
			}
			if truncated != tc.want {
				t.Errorf("truncated = %v, want %v", truncated, tc.want)
			}
			if tc.limit > 0 {
				if got := countRows(t, raw, "errorIssues"); got > tc.limit {
					t.Errorf("items = %d, want at most the --limit %d", got, tc.limit)
				}
			}
		})
	}
}

func TestListAnomalies_truncationSignal(t *testing.T) {
	rt := seqFake(
		`{"anomalies":[{"metricSet":"apps/x/crashRateMetricSet"}],"nextPageToken":"P2"}`,
		`{"anomalies":[{"metricSet":"apps/x/anrRateMetricSet"}],"nextPageToken":"P3"}`,
	)
	_, truncated, err := vitals.ListAnomalies(context.Background(),
		&http.Client{Transport: rt}, "com.example.app", vitals.AnomalyListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListAnomalies: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true (--limit 1 with a nextPageToken left)")
	}
}
