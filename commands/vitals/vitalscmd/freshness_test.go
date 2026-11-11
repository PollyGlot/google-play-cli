package vitalscmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// freshnessFake answers the metric set's `.get` with the given freshness body
// and its `:query` with one row, so a test sees both legs of Execute.
func freshnessFake(describe string) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if c.Method == http.MethodGet {
			return http.StatusOK, describe, true
		}
		return http.StatusOK, presetRowsBody, true
	})
}

// TestExecute_windowEndsAtFreshness pins #768: the API rejects a timeline whose
// end is past the set's freshness, so the window must end at latestEndTime and
// --since counts back from there, not from today.
func TestExecute_windowEndsAtFreshness(t *testing.T) {
	const fresh = `{"freshnessInfo":{"freshnesses":[` +
		`{"aggregationPeriod":"HOURLY","latestEndTime":{"year":2020,"month":1,"day":10,"hours":9,"timeZone":{"id":"UTC"}}},` +
		`{"aggregationPeriod":"DAILY","latestEndTime":{"year":2020,"month":1,"day":10,"timeZone":{"id":"America/Los_Angeles"}}}]}}`
	for _, tc := range []struct {
		period, since, start, end string
	}{
		{"DAILY", "7d", `"startTime":{"year":2020,"month":1,"day":3}`, `"endTime":{"year":2020,"month":1,"day":10}`},
		{"HOURLY", "24h", `"startTime":{"year":2020,"month":1,"day":9,"hours":9}`, `"endTime":{"year":2020,"month":1,"day":10,"hours":9}`},
	} {
		t.Run(tc.period, func(t *testing.T) {
			fake := freshnessFake(fresh)
			rc := newRC(t, fake)
			_, err := Execute(rc, Params{Set: set(t, "crashrate"), Package: "com.example.app", Period: tc.period, Since: tc.since})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			body := string(lastCall(t, fake).Body)
			if !strings.Contains(body, tc.start) || !strings.Contains(body, tc.end) {
				t.Errorf("query window not anchored on the %s freshness:\n%s", tc.period, body)
			}
		})
	}
}

// TestExecute_noFreshnessKeepsToday: a descriptor without an entry for the
// requested period leaves the window anchored on today, as before.
func TestExecute_noFreshnessKeepsToday(t *testing.T) {
	fake := freshnessFake(`{"freshnessInfo":{"freshnesses":[]}}`)
	rc := newRC(t, fake)
	if _, err := Execute(rc, Params{Set: set(t, "crashrate"), Package: "com.example.app"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if body := string(lastCall(t, fake).Body); strings.Contains(body, `"year":2020`) {
		t.Errorf("window must not move without a freshness entry: %s", body)
	}
}
