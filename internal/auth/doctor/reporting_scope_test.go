package doctor_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

// TestCheckReportingScope_happyPath_observesReportingScope asserts the new
// reporting-scope check (#49) mints a token for the playdeveloperreporting
// scope and confirms the exchange requested it — the read-only vitals service
// uses a DISTINCT scope, and `auth doctor` now reports whether a token can be
// minted for it.
func TestCheckReportingScope_happyPath_observesReportingScope(t *testing.T) {
	sa := makeSignedSA(t)
	rt := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	hc, obs := scopeWired(rt)

	results := doctor.Run(context.Background(), sa, hc, doctor.CheckReportingScope(obs))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if !results[0].Passed || results[0].Skipped {
		t.Errorf("result = %+v, want Passed", results[0])
	}
	found := false
	for _, s := range obs.Scopes() {
		if s == token.ReportingScope {
			found = true
		}
	}
	if !found {
		t.Errorf("observed scopes %v missing the reporting scope", obs.Scopes())
	}
}

// TestCheckReportingScope_nilObserver_reportsWiringBug mirrors CheckScope: a
// missing observer is a gplay wiring bug, not a credential problem.
func TestCheckReportingScope_nilObserver_reportsWiringBug(t *testing.T) {
	sa := makeSignedSA(t)
	rt := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`{"access_token":"a","expires_in":3600}`))}, nil
	})
	results := doctor.Run(context.Background(), sa, &http.Client{Transport: rt}, doctor.CheckReportingScope(nil))
	if results[0].Passed {
		t.Error("nil observer must fail the check")
	}
}

// TestDefaultChecks_includesReportingScope asserts the reporting-scope check is
// part of the default `auth doctor` chain, after the androidpublisher scope
// check.
func TestDefaultChecks_includesReportingScope(t *testing.T) {
	checks := doctor.DefaultChecks(nil)
	if len(checks) != 4 {
		t.Fatalf("DefaultChecks len = %d, want 4 (sa, mint, scope, reporting-scope)", len(checks))
	}
	if !contains(checks[3].Name, "playdeveloperreporting") {
		t.Errorf("last default check = %q, want the reporting-scope check", checks[3].Name)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
