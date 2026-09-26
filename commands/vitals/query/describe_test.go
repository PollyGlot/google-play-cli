package query

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const describeBody = `{"name":"apps/com.example.app/crashRateMetricSet","freshnessInfo":{"freshnesses":[{"aggregationPeriod":"DAILY","latestEndTime":{"year":2026,"month":9,"day":12,"timeZone":{"id":"America/Los_Angeles"}}}]}}`

// describeRT records every non-token request so the test can assert exactly
// one GET went out and no POST.
type describeRT struct {
	queryRT
	methods []string
}

func (r *describeRT) serve(req *http.Request) (*http.Response, error) {
	if testkit.IsTokenRequest(req) {
		return r.queryRT.serve(req)
	}
	// Not delegated: queryRT reads a request body, and a GET has none.
	r.methods = append(r.methods, req.Method)
	r.queryURL = req.URL.String()
	return testkit.Response(http.StatusOK, r.respBody), nil
}

// TestRun_describe_getsTheMetricSet proves `vitals query <set> --describe`
// calls the set's `.get` (a single GET on the bare resource, no `:query` POST),
// keeps the reporting scope, passes the body through verbatim and projects the
// freshness table (#545).
func TestRun_describe_getsTheMetricSet(t *testing.T) {
	rt := &describeRT{queryRT: queryRT{t: t, respBody: describeBody}}
	rc, _, stderr := newRC(t, testkit.RoundTripFunc(rt.serve))

	r, err := Run(rc, Input{MetricSet: "crashrate", Package: "com.example.app", Describe: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(rt.methods, ","); got != "GET" {
		t.Errorf("API calls = %q, want exactly one GET (no POST)", got)
	}
	if want := "/apps/com.example.app/crashRateMetricSet"; !strings.HasSuffix(rt.queryURL, want) {
		t.Errorf("URL = %q, want suffix %q", rt.queryURL, want)
	}
	if strings.Contains(rt.queryURL, ":query") {
		t.Errorf("--describe must not hit :query: %q", rt.queryURL)
	}
	if rt.tokenScopes != token.ReportingScope {
		t.Errorf("token scope = %q, want %q", rt.tokenScopes, token.ReportingScope)
	}

	p, ok := r.(vitalscmd.DescribePayload)
	if !ok {
		t.Fatalf("renderable = %T, want DescribePayload", r)
	}
	if len(p.Freshnesses) != 1 || p.Freshnesses[0].LatestEndTime != "2026-09-12" || p.Freshnesses[0].TimeZone != "America/Los_Angeles" {
		t.Errorf("freshnesses = %+v", p.Freshnesses)
	}

	var buf bytes.Buffer
	if err := p.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(buf.String(), `"freshnessInfo"`) {
		t.Errorf("JSON pass-through lost the descriptor: %s", buf.String())
	}
	buf.Reset()
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	for _, want := range []string{"PERIOD", "LATEST_END_TIME", "TIMEZONE", "DAILY", "2026-09-12", "America/Los_Angeles"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("table missing %q:\n%s", want, buf.String())
		}
	}
	// The freshness IS the output: no timeline note is written to stderr.
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

// TestRun_describe_rejectsWindowFlags: a window flag set alongside --describe
// is CLI misuse (exit 2) that names the offending flags, and no request is
// sent at all.
func TestRun_describe_rejectsWindowFlags(t *testing.T) {
	rt := &describeRT{queryRT: queryRT{t: t, respBody: describeBody}}
	rc, _, _ := newRC(t, testkit.RoundTripFunc(rt.serve))
	_, err := Run(rc, Input{
		MetricSet: "crashrate", Package: "com.example.app", Describe: true,
		Since: "7d", Dimensions: []string{"versionCode"}, WindowFlags: []string{"dimensions", "since"},
	})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(err.Error(), "--dimensions, --since do not apply") {
		t.Errorf("error must name the clashing flags: %v", err)
	}
	if len(rt.methods) != 0 {
		t.Errorf("no request must be sent on a flag clash, got %v", rt.methods)
	}
}

// TestNewCommand_describeFlagClashDetectedFromCobra drives the real cobra
// command so the Changed-state plumbing is exercised: --since left at its
// default is not a clash, --since set explicitly is.
func TestNewCommand_describeFlagClashDetectedFromCobra(t *testing.T) {
	cmd := NewCommand(kernel.Boot{})
	if err := cmd.ParseFlags([]string{"--describe", "--since", "7d", "--period", "HOURLY"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got := vitalscmd.ChangedFlags(cmd, windowFlags...)
	if strings.Join(got, ",") != "period,since" {
		t.Errorf("changed window flags = %v, want [period since]", got)
	}
	cmd = NewCommand(kernel.Boot{})
	if err := cmd.ParseFlags([]string{"--describe"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got := vitalscmd.ChangedFlags(cmd, windowFlags...); len(got) != 0 {
		t.Errorf("defaults must not count as a clash, got %v", got)
	}
}
