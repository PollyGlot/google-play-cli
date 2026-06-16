package vitalscmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

type presetRT struct {
	mu       sync.Mutex
	queryURL string
	body     string
}

func (r *presetRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := http.Header{"Content-Type": []string{"application/json"}}
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`{"access_token":"a","token_type":"Bearer","expires_in":3600}`))}, nil
	}
	r.queryURL = req.URL.String()
	b, _ := io.ReadAll(req.Body)
	r.body = string(b)
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`{"rows":[{"startTime":{"year":2026,"month":6,"day":1},"metrics":[{"metric":"crashRate","decimalValue":{"value":"0.01"}}]}]}`))}, nil
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
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
	rt := &presetRT{}
	rc := newRC(t, rt)
	if _, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if !strings.HasSuffix(rt.queryURL, "/apps/com.example.app/crashRateMetricSet:query") {
		t.Errorf("URL = %q", rt.queryURL)
	}
	if !strings.Contains(rt.body, `"crashRate"`) {
		t.Errorf("preset must default to the primary crashRate metric: %s", rt.body)
	}
}

func TestRunPreset_anr_hitsAnrSet(t *testing.T) {
	rt := &presetRT{}
	rc := newRC(t, rt)
	if _, err := runPreset(rc, set(t, "anrrate"), presetInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if !strings.HasSuffix(rt.queryURL, "/apps/com.example.app/anrRateMetricSet:query") {
		t.Errorf("URL = %q, want the anrRateMetricSet resource", rt.queryURL)
	}
}

func TestRunPreset_byAndVersion(t *testing.T) {
	rt := &presetRT{}
	rc := newRC(t, rt)
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", By: "device", Version: "123"})
	if err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	// --by device → the deviceModel dimension.
	if !strings.Contains(rt.body, `"deviceModel"`) {
		t.Errorf("--by device must map to deviceModel: %s", rt.body)
	}
	// --version 123 → a versionCode filter.
	if !strings.Contains(rt.body, `versionCode = 123`) {
		t.Errorf("--version must produce a versionCode filter: %s", rt.body)
	}
}

func TestRunPreset_unknownBy_isUsageError(t *testing.T) {
	rc := newRC(t, &presetRT{})
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", By: "phase-of-moon"})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}

func TestRunPreset_badVersion_isUsageError(t *testing.T) {
	rc := newRC(t, &presetRT{})
	_, err := runPreset(rc, set(t, "crashrate"), presetInput{Package: "com.example.app", Version: "not-a-number"})
	if got := exitCode(t, err); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
}

// TestPresets_referenceRealMetricSets asserts every declared preset points at a
// registered metric set, so NewPresetCommand never panics in production.
func TestPresets_referenceRealMetricSets(t *testing.T) {
	for _, spec := range Presets {
		if _, ok := vitals.MetricSetByName(spec.Set); !ok {
			t.Errorf("preset %q references unknown metric set %q", spec.Use, spec.Set)
		}
	}
}

// TestPresets_coverEveryMetricSet asserts every queryable metric set has an
// opinionated preset (#260 completes the set: all seven). A new metric set added
// to the registry without a preset fails here.
func TestPresets_coverEveryMetricSet(t *testing.T) {
	covered := map[string]bool{}
	for _, spec := range Presets {
		covered[spec.Set] = true
	}
	for _, ms := range vitals.MetricSets() {
		if !covered[ms.Name] {
			t.Errorf("metric set %q has no preset command", ms.Name)
		}
	}
}

// TestRunPreset_everyPresetHitsItsResource is the table-driven proof that each
// preset issues its `:query` to the metric set's own REST resource (#260: the
// five additional metric sets, alongside crashes/anr).
func TestRunPreset_everyPresetHitsItsResource(t *testing.T) {
	for _, spec := range Presets {
		spec := spec
		t.Run(spec.Use, func(t *testing.T) {
			rt := &presetRT{}
			rc := newRC(t, rt)
			ms := set(t, spec.Set)
			if _, err := runPreset(rc, ms, presetInput{Package: "com.example.app"}); err != nil {
				t.Fatalf("runPreset: %v", err)
			}
			want := "/apps/com.example.app/" + ms.Resource + ":query"
			if !strings.HasSuffix(rt.queryURL, want) {
				t.Errorf("preset %q URL = %q, want suffix %q", spec.Use, rt.queryURL, want)
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
