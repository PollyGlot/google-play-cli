package vitals_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const reportingRoot = "https://playdeveloperreporting.googleapis.com/v1beta1/apps/com.example.app"

func lastCall(t *testing.T, fake *testkit.Fake) testkit.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no request reached the transport")
	}
	return calls[len(calls)-1]
}

func TestQueryAbsoluteURLPerMetricSet(t *testing.T) {
	sets := append(vitals.MetricSets(), vitals.ErrorCountSet())
	for _, set := range sets {
		t.Run(set.Name, func(t *testing.T) {
			rt := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
			if _, err := vitals.Query(context.Background(), &http.Client{Transport: rt}, set, "com.example.app", nil); err != nil {
				t.Fatalf("Query: %v", err)
			}
			c := lastCall(t, rt)
			want := reportingRoot + "/" + set.Resource + ":query"
			if c.URL != want {
				t.Errorf("URL = %q, want %q", c.URL, want)
			}
			if c.Method != http.MethodPost {
				t.Errorf("verb = %q, want POST", c.Method)
			}
		})
	}
}

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
			rt := testkit.NewFake(testkit.Any(http.StatusOK, `{}`))
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			c := lastCall(t, rt)
			if c.URL != tc.want {
				t.Errorf("URL = %q, want %q", c.URL, tc.want)
			}
			if c.Method != http.MethodGet {
				t.Errorf("verb = %q, want GET", c.Method)
			}
		})
	}
}
