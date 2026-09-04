// Migration proof for #519: vitals now takes verb and URL from
// internal/apiregistry. The other vitals tests assert paths and query params;
// none of them pinned the HOST, which matters here because Play Developer
// Reporting is a separate Google service (playdeveloperreporting.googleapis.com
// with a /v1beta1 base, not androidpublisher/v3).
//
// This file also closes the loop the metric-set registry left open: since the
// query URL is now resolved from MetricSet.QueryMethodID(), the same string
// integrity_test.go anchors to the Discovery index, the two registries can no
// longer disagree about where a `:query` goes.
package vitals_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

const reportingRoot = "https://playdeveloperreporting.googleapis.com/v1beta1/apps/com.example.app"

type urlRT struct{ url, verb string }

func (r *urlRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.url = req.URL.String()
	r.verb = req.Method
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

// TestQueryAbsoluteURLPerMetricSet pins the resolved `:query` endpoint of every
// declared metric set plus the errors.counts set, which lives outside
// metricSets and is therefore the case a package-level resolution would have
// missed.
func TestQueryAbsoluteURLPerMetricSet(t *testing.T) {
	sets := append(vitals.MetricSets(), vitals.ErrorCountSet())
	for _, set := range sets {
		t.Run(set.Name, func(t *testing.T) {
			rt := &urlRT{}
			if _, err := vitals.Query(context.Background(), &http.Client{Transport: rt}, set, "com.example.app", nil); err != nil {
				t.Fatalf("Query: %v", err)
			}
			want := reportingRoot + "/" + set.Resource + ":query"
			if rt.url != want {
				t.Errorf("URL = %q, want %q", rt.url, want)
			}
			if rt.verb != http.MethodPost {
				t.Errorf("verb = %q, want POST", rt.verb)
			}
		})
	}
}

// TestListAndSearchAbsoluteURLs pins the three paginated GET reads.
func TestListAndSearchAbsoluteURLs(t *testing.T) {
	cases := []struct {
		name string
		call func(*http.Client) error
		want string
	}{
		{
			name: "anomalies.list",
			call: func(hc *http.Client) error {
				_, _, err := vitals.ListAnomalies(context.Background(), hc, "com.example.app", vitals.AnomalyListOptions{})
				return err
			},
			want: reportingRoot + "/anomalies?pageSize=100",
		},
		{
			name: "errors.issues.search",
			call: func(hc *http.Client) error {
				_, _, err := vitals.SearchErrorIssues(context.Background(), hc, "com.example.app", vitals.SearchOptions{})
				return err
			},
			want: reportingRoot + "/errorIssues:search?pageSize=1000",
		},
		{
			name: "errors.reports.search",
			call: func(hc *http.Client) error {
				_, _, err := vitals.SearchErrorReports(context.Background(), hc, "com.example.app", vitals.SearchOptions{})
				return err
			},
			want: reportingRoot + "/errorReports:search?pageSize=100",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &urlRT{}
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if rt.url != tc.want {
				t.Errorf("URL = %q, want %q", rt.url, tc.want)
			}
			if rt.verb != http.MethodGet {
				t.Errorf("verb = %q, want GET", rt.verb)
			}
		})
	}
}
