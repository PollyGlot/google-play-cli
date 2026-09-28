package doctor_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	authdoctor "github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_allPassed_golden freezes the green checklist: a bare array
// of CheckResult with no hint key (omitempty), the shape scripts branch on.
func TestRenderJSON_allPassed_golden(t *testing.T) {
	p := doctor.Payload{Results: []authdoctor.CheckResult{
		{Name: "Service account JSON is valid", Passed: true, ExitCode: 10},
		{Name: "OAuth2 access token can be minted", Passed: true, ExitCode: 10},
		{Name: "Token carries the androidpublisher scope", Passed: true, ExitCode: 10},
		{Name: "Token can be minted for the playdeveloperreporting scope", Passed: true, ExitCode: 10},
		{Name: "Service account can edit com.example.app", Passed: true, ExitCode: 11},
	}}
	outputtest.GoldenJSON(t, "all_passed.json.golden", p)
}

// TestRenderJSON_failedThenSkipped_golden freezes the red chain: one failure
// carrying its hint, every later check reported skipped. The hint is free
// text quoting an error, hence the <, > and & that must reach stdout raw.
func TestRenderJSON_failedThenSkipped_golden(t *testing.T) {
	p := doctor.Payload{Results: []authdoctor.CheckResult{
		{Name: "Service account JSON is valid", Passed: true, ExitCode: 10},
		{Name: "OAuth2 access token can be minted", ExitCode: 10, Hint: "token exchange refused <invalid_grant> & key revoked; run `gplay auth login`"},
		{Name: "Token carries the androidpublisher scope", Skipped: true, ExitCode: 10},
		{Name: "Token can be minted for the playdeveloperreporting scope", Skipped: true, ExitCode: 10},
		{Name: "Service account can edit com.example.app", Skipped: true, ExitCode: 11},
	}}
	outputtest.GoldenJSON(t, "failed_then_skipped.json.golden", p)
}
