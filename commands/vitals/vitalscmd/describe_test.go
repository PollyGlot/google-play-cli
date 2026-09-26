package vitalscmd

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// describeRT answers the token exchange and records every API call's verb and
// URL, so a preset test can assert "one GET, no POST".
type describeRT struct {
	presetRT
	calls []string // "VERB path"
}

func (r *describeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return r.presetRT.RoundTrip(req)
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	h := http.Header{"Content-Type": []string{"application/json"}}
	body := `{"name":"apps/com.example.app/anrRateMetricSet","freshnessInfo":{"freshnesses":[{"aggregationPeriod":"DAILY","latestEndTime":{"year":2026,"month":9,"day":12,"timeZone":{"id":"America/Los_Angeles"}}}]}}`
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestRunPreset_describe_getsTheMetricSet proves a preset's --describe reaches
// the set's `.get`: exactly one GET on the bare resource, never the `:query`
// POST (#545).
func TestRunPreset_describe_getsTheMetricSet(t *testing.T) {
	rt := &describeRT{}
	rc := newRC(t, rt)
	r, err := runPreset(rc, set(t, "anrrate"), presetInput{Package: "com.example.app", Describe: true})
	if err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "GET /v1beta1/apps/com.example.app/anrRateMetricSet" {
		t.Errorf("API calls = %v, want exactly one GET on the anrRateMetricSet resource", rt.calls)
	}
	p, ok := r.(DescribePayload)
	if !ok {
		t.Fatalf("renderable = %T, want DescribePayload", r)
	}
	if len(p.Freshnesses) != 1 || p.Freshnesses[0].Period != "DAILY" {
		t.Errorf("freshnesses = %+v", p.Freshnesses)
	}
}

// TestRunPreset_describe_rejectsWindowFlags: --by/--version-code/--since/--period
// set alongside --describe is usage misuse, caught before any request.
func TestRunPreset_describe_rejectsWindowFlags(t *testing.T) {
	rt := &describeRT{}
	rc := newRC(t, rt)
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{
		Package: "com.example.app", Describe: true, By: "device", WindowFlags: []string{"by"},
	})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "--by does not apply") {
		t.Errorf("error must name the clashing flag: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("no request must be sent on a flag clash, got %v", rt.calls)
	}
}

func TestRejectWindowFlags(t *testing.T) {
	if err := RejectWindowFlags(false, []string{"since"}); err != nil {
		t.Errorf("without --describe the window flags are fine, got %v", err)
	}
	if err := RejectWindowFlags(true, nil); err != nil {
		t.Errorf("--describe alone is fine, got %v", err)
	}
	err := RejectWindowFlags(true, []string{"since", "period"})
	if err == nil || !strings.Contains(err.Error(), "--since, --period do not apply") {
		t.Errorf("want both flags named with a plural verb, got %v", err)
	}
}

// TestPresetCommand_describeFlagIsRegistered drives the real cobra command:
// the flag exists on every preset and its Changed state feeds ChangedFlags.
func TestPresetCommand_describeFlagIsRegistered(t *testing.T) {
	for _, spec := range Presets {
		cmd := NewPresetCommand(kernel.Boot{}, spec)
		if cmd.Flags().Lookup(DescribeFlag) == nil {
			t.Errorf("preset %s: missing --%s", spec.Use, DescribeFlag)
			continue
		}
		if err := cmd.ParseFlags([]string{"--describe", "--version-code", "12"}); err != nil {
			t.Fatalf("preset %s ParseFlags: %v", spec.Use, err)
		}
		if got := ChangedFlags(cmd, presetWindowFlags...); strings.Join(got, ",") != "version-code" {
			t.Errorf("preset %s: changed window flags = %v, want [version-code]", spec.Use, got)
		}
	}
}
