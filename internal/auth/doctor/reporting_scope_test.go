package doctor_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestCheckReportingScope_happyPath_observesReportingScope(t *testing.T) {
	sa := makeSignedSA(t)
	hc, obs := scopeWired(testkit.NewFake())

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

func TestScopeChain_reportingNotContaminatedByAndroidPublisher(t *testing.T) {
	sa := makeSignedSA(t)
	hc, obs := scopeWired(testkit.NewFake())

	results := doctor.Run(context.Background(), sa, hc,
		doctor.CheckScope(obs), doctor.CheckReportingScope(obs))
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for i, r := range results {
		if !r.Passed {
			t.Errorf("check[%d] failed: %+v", i, r)
		}
	}
	found := false
	for _, s := range obs.Scopes() {
		if s == token.ReportingScope {
			found = true
		}
	}
	if !found {
		t.Errorf("observed scopes %v should reflect the reporting exchange, not the prior androidpublisher one", obs.Scopes())
	}
}

func TestCheckReportingScope_nilObserver_reportsWiringBug(t *testing.T) {
	sa := makeSignedSA(t)
	results := doctor.Run(context.Background(), sa, &http.Client{Transport: testkit.NewFake()}, doctor.CheckReportingScope(nil))
	if results[0].Passed {
		t.Error("nil observer must fail the check")
	}
}

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
