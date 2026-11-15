package errorscmd

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// hourlyFresh is an errorCountMetricSet descriptor whose HOURLY data ends at
// 2020-01-10 09:00 UTC: older than any clock the test runs on, so an hour
// window must end there and nowhere else.
const hourlyFresh = `{"freshnessInfo":{"freshnesses":[` +
	`{"aggregationPeriod":"HOURLY","latestEndTime":{"year":2020,"month":1,"day":10,"hours":9,"timeZone":{"id":"UTC"}}},` +
	`{"aggregationPeriod":"DAILY","latestEndTime":{"year":2020,"month":1,"day":10,"timeZone":{"id":"America/Los_Angeles"}}}]}}`

// windowFake answers the errorCount descriptor with hourlyFresh and every
// search with an empty page.
func windowFake() *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if strings.HasSuffix(c.Path, "/errorCountMetricSet") {
			return http.StatusOK, hourlyFresh, true
		}
		return http.StatusOK, `{}`, true
	})
}

// intervalOf returns the interval.* query params of the last call: the whole
// window as the search sends it.
func intervalOf(t *testing.T, fake *testkit.Fake) url.Values {
	t.Helper()
	q, err := url.ParseQuery(lastCall(t, fake).Query)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	out := url.Values{}
	for k, v := range q {
		if strings.HasPrefix(k, "interval.") {
			out[k] = v
		}
	}
	return out
}

func dateParams(start, end time.Time) url.Values {
	v := url.Values{}
	for prefix, t := range map[string]time.Time{"interval.startTime": start, "interval.endTime": end} {
		v.Set(prefix+".year", strconv.Itoa(t.Year()))
		v.Set(prefix+".month", strconv.Itoa(int(t.Month())))
		v.Set(prefix+".day", strconv.Itoa(t.Day()))
	}
	return v
}

func hourParams(start, end time.Time) url.Values {
	v := dateParams(start, end)
	v.Set("interval.startTime.hours", strconv.Itoa(start.Hour()))
	v.Set("interval.endTime.hours", strconv.Itoa(end.Hour()))
	return v
}

// TestSearchWindow pins #775: an hour window is sent with its hours and ends at
// the newest hour of the errorCount set, a day window keeps its UTC-midnight
// alignment ending today. Both search views share the window.
func TestSearchWindow(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	views := map[string]func(*testing.T, string) *testkit.Fake{
		"issues": func(t *testing.T, since string) *testkit.Fake {
			t.Helper()
			rc, fake, _ := newFakeRC(t, windowFake())
			if _, err := runIssues(rc, issuesInput{Package: "com.example.app", Since: since}); err != nil {
				t.Fatalf("runIssues: %v", err)
			}
			return fake
		},
		"reports": func(t *testing.T, since string) *testkit.Fake {
			t.Helper()
			rc, fake, _ := newFakeRC(t, windowFake())
			if _, err := runReports(rc, reportsInput{Package: "com.example.app", Since: since}); err != nil {
				t.Fatalf("runReports: %v", err)
			}
			return fake
		},
	}
	for _, tc := range []struct {
		since string
		want  url.Values
	}{
		{"6h", hourParams(time.Date(2020, 1, 10, 3, 0, 0, 0, time.UTC), time.Date(2020, 1, 10, 9, 0, 0, 0, time.UTC))},
		{"10h", hourParams(time.Date(2020, 1, 9, 23, 0, 0, 0, time.UTC), time.Date(2020, 1, 10, 9, 0, 0, 0, time.UTC))},
		{"9h", hourParams(time.Date(2020, 1, 10, 0, 0, 0, 0, time.UTC), time.Date(2020, 1, 10, 9, 0, 0, 0, time.UTC))},
		{"7d", dateParams(today.AddDate(0, 0, -7), today)},
	} {
		for view, run := range views {
			t.Run(view+"/"+tc.since, func(t *testing.T) {
				got := intervalOf(t, run(t, tc.since))
				if got.Encode() != tc.want.Encode() {
					t.Errorf("--since %s interval:\n got %s\nwant %s", tc.since, got.Encode(), tc.want.Encode())
				}
			})
		}
	}
}
