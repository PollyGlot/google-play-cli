package vitals_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// TestSearchErrorIssues_truncationSignal pins the signal behind the stderr
// warning of PRD #446 / #451: `truncated` is true only when --limit stopped the
// loop while the server STILL had a nextPageToken to give.
//
// The three cases are the whole decision table:
//   - limit hit mid-stream, token remaining → truncated (the list is a prefix)
//   - limit hit exactly on the last page, no token → NOT truncated (the cap and
//     the truth coincide; warning there would be a lie)
//   - no limit at all → NOT truncated (gplay exhausts the token by default)
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
			// A server that hands back MORE than the cap in one page, with no
			// token left: the slice below drops rows we were already holding,
			// so this is a truncation even though nothing remains upstream.
			// Keying the signal on the token alone missed exactly this.
			name: "single-page-overshoots-the-limit",
			pages: []string{
				`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"},{"type":"ANR"},{"type":"ANR"},{"type":"NON_FATAL"}]}`,
			},
			limit: 3,
			want:  true,
		},
		{
			// Same overshoot, one page deep into a stream: the drop happens on
			// the second page and there is still a token behind it.
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
			rt := &seqRT{pages: tc.pages}
			raw, truncated, err := vitals.SearchErrorIssues(context.Background(),
				&http.Client{Transport: rt}, "com.example.app", vitals.SearchOptions{Limit: tc.limit})
			if err != nil {
				t.Fatalf("SearchErrorIssues: %v", err)
			}
			if truncated != tc.want {
				t.Errorf("truncated = %v, want %v", truncated, tc.want)
			}
			// The signal rides beside the payload, never inside it: the
			// envelope carries the items and nothing else (ADR-0003).
			if tc.limit > 0 {
				if got := countRows(t, raw, "errorIssues"); got > tc.limit {
					t.Errorf("items = %d, want at most the --limit %d", got, tc.limit)
				}
			}
		})
	}
}

// TestListAnomalies_truncationSignal repeats the mid-stream case on the other
// paginated Reporting surface, so the signal is not an errors-only accident.
func TestListAnomalies_truncationSignal(t *testing.T) {
	rt := &seqRT{pages: []string{
		`{"anomalies":[{"metricSet":"apps/x/crashRateMetricSet"}],"nextPageToken":"P2"}`,
		`{"anomalies":[{"metricSet":"apps/x/anrRateMetricSet"}],"nextPageToken":"P3"}`,
	}}
	_, truncated, err := vitals.ListAnomalies(context.Background(),
		&http.Client{Transport: rt}, "com.example.app", vitals.AnomalyListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListAnomalies: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true (--limit 1 with a nextPageToken left)")
	}
}
