package vitalscmd

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const presetRowsBody = `{"rows":[{"startTime":{"year":2026,"month":6,"day":1},"metrics":[{"metric":"crashRate","decimalValue":{"value":"0.01"}}]}]}`

func newPresetFake() *testkit.Fake {
	return testkit.NewFake(testkit.Any(http.StatusOK, presetRowsBody))
}

func lastCall(t *testing.T, fake *testkit.Fake) testkit.Call {
	t.Helper()
	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no API call recorded")
	}
	return calls[len(calls)-1]
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc
}

func set(t *testing.T, name string) vitals.MetricSet {
	t.Helper()
	s, ok := vitals.MetricSetByName(name)
	if !ok {
		t.Fatalf("metric set %q not in registry", name)
	}
	return s
}

func TestRunPreset_crashes_defaultsToPrimaryMetric(t *testing.T) {
	fake := newPresetFake()
	rc := newRC(t, fake)
	if _, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	last := lastCall(t, fake)
	if !strings.HasSuffix(last.URL, "/apps/com.example.app/crashRateMetricSet:query") {
		t.Errorf("URL = %q", last.URL)
	}
	if !strings.Contains(string(last.Body), `"crashRate"`) {
		t.Errorf("preset must default to the primary crashRate metric: %s", last.Body)
	}
}

func TestRunPreset_anr_hitsAnrSet(t *testing.T) {
	fake := newPresetFake()
	rc := newRC(t, fake)
	if _, err := runPreset(rc, set(t, "anrrate"), presetInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if last := lastCall(t, fake); !strings.HasSuffix(last.URL, "/apps/com.example.app/anrRateMetricSet:query") {
		t.Errorf("URL = %q, want the anrRateMetricSet resource", last.URL)
	}
}

func TestRunPreset_byAndVersion(t *testing.T) {
	fake := newPresetFake()
	rc := newRC(t, fake)
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", By: "device", VersionCode: "123"})
	if err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	body := string(lastCall(t, fake).Body)
	if !strings.Contains(body, `"deviceModel"`) {
		t.Errorf("--by device must map to deviceModel: %s", body)
	}
	if !strings.Contains(body, `versionCode = 123`) {
		t.Errorf("--version-code must produce a versionCode filter: %s", body)
	}
}

func TestRunPreset_unknownBy_isUsageError(t *testing.T) {
	rc := newRC(t, newPresetFake())
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", By: "phase-of-moon"})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}

func TestRunPreset_badVersion_isUsageError(t *testing.T) {
	rc := newRC(t, newPresetFake())
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", VersionCode: "not-a-number"})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}

func TestPresets_referenceRealMetricSets(t *testing.T) {
	for _, spec := range Presets {
		if _, ok := vitals.MetricSetByName(spec.Set); !ok {
			t.Errorf("preset %q references unknown metric set %q", spec.Use, spec.Set)
		}
	}
}

var presetExempt = map[string]bool{
	"anonrssandswapmemoryusage": true,
	"bitmapmemoryusage":         true,
}

func TestPresets_coverEveryMetricSet(t *testing.T) {
	covered := map[string]bool{}
	for _, spec := range Presets {
		covered[spec.Set] = true
	}
	for _, ms := range vitals.MetricSets() {
		if !covered[ms.Name] && !presetExempt[ms.Name] {
			t.Errorf("metric set %q has no preset command", ms.Name)
		}
	}
	for name := range presetExempt {
		if covered[name] {
			t.Errorf("metric set %q is exempt yet has a preset: drop the exemption", name)
		}
	}
}

func TestRunPreset_everyPresetHitsItsResource(t *testing.T) {
	for _, spec := range Presets {
		t.Run(spec.Use, func(t *testing.T) {
			fake := newPresetFake()
			rc := newRC(t, fake)
			ms := set(t, spec.Set)
			if _, err := runPreset(rc, ms, presetInput{Package: "com.example.app"}); err != nil {
				t.Fatalf("runPreset: %v", err)
			}
			want := "/apps/com.example.app/" + ms.Resource + ":query"
			if last := lastCall(t, fake); !strings.HasSuffix(last.URL, want) {
				t.Errorf("preset %q URL = %q, want suffix %q", spec.Use, last.URL, want)
			}
		})
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err %T has no ExitCode", err)
	}
	return coder.ExitCode()
}

func TestRunPreset_userCohort_reachesTheQueryBody(t *testing.T) {
	fake := newPresetFake()
	rc := newRC(t, fake)
	if _, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", UserCohort: "APP_TESTERS"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	last := fake.Calls()[len(fake.Calls())-1]
	var body map[string]any
	if err := json.Unmarshal(last.Body, &body); err != nil {
		t.Fatalf("decode %s body: %v", last.Path, err)
	}
	if body["userCohort"] != "APP_TESTERS" {
		t.Errorf("userCohort = %v, want APP_TESTERS (body %s)", body["userCohort"], last.Body)
	}
}
