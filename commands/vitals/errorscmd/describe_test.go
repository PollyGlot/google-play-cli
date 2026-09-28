package errorscmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
)

// TestRunCounts_describe_getsErrorCountSet proves the tenth `.get`
// (errors.counts.get) is reachable: `vitals errors counts --describe` issues a
// GET on the bare errorCountMetricSet resource, not the `:query` POST (#545).
func TestRunCounts_describe_getsErrorCountSet(t *testing.T) {
	rc, fake, _ := newRC(t, `{"name":"apps/com.example.app/errorCountMetricSet","freshnessInfo":{"freshnesses":[{"aggregationPeriod":"DAILY","latestEndTime":{"year":2026,"month":9,"day":12,"timeZone":{"id":"America/Los_Angeles"}}}]}}`)
	r, err := runCounts(rc, countsInput{Package: "com.example.app", Describe: true})
	if err != nil {
		t.Fatalf("runCounts: %v", err)
	}
	last := lastCall(t, fake)
	if last.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", last.Method)
	}
	if !strings.HasSuffix(last.URL, "/apps/com.example.app/errorCountMetricSet") {
		t.Errorf("URL = %q, want the bare errorCountMetricSet resource", last.URL)
	}
	p, ok := r.(vitalscmd.DescribePayload)
	if !ok {
		t.Fatalf("renderable = %T, want DescribePayload", r)
	}
	if len(p.Freshnesses) != 1 || p.Freshnesses[0].LatestEndTime != "2026-09-12" {
		t.Errorf("freshnesses = %+v", p.Freshnesses)
	}
}

func TestRunCounts_describe_rejectsWindowFlags(t *testing.T) {
	rc, fake, _ := newRC(t, `{}`)
	_, err := runCounts(rc, countsInput{Package: "com.example.app", Describe: true, Since: "7d", WindowFlags: []string{"since"}})
	if err == nil || !strings.Contains(err.Error(), "--since does not apply") {
		t.Errorf("want a usage error naming --since, got %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("no request must be sent on a flag clash, got %q", calls[0].URL)
	}
}
