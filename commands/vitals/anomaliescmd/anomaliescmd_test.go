package anomaliescmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
)

type anomRT struct {
	mu      sync.Mutex
	body    string
	lastURL string
	method  string
}

func (r *anomRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := http.Header{"Content-Type": []string{"application/json"}}
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`{"access_token":"a","token_type":"Bearer","expires_in":3600}`))}, nil
	}
	r.lastURL = req.URL.String()
	r.method = req.Method
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(r.body))}, nil
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
		"type": "service_account", "project_id": "p",
		"private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, body string) (*kernel.RunContext, *anomRT, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rt := &anomRT{body: body}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, rt, &stderr
}

func TestRun_listsAnomalies_withSinceWindow(t *testing.T) {
	body := `{"anomalies":[{"metricSet":"apps/com.example.app/crashRateMetricSet","metric":{"metric":"crashRate","decimalValue":{"value":"0.08"}},"timelineSpec":{"startTime":{"year":2026,"month":6,"day":10},"endTime":{"year":2026,"month":6,"day":12}}}]}`
	rc, rt, _ := newRC(t, body)
	r, err := Run(rc, Input{Package: "com.example.app", Since: "90d"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.method != http.MethodGet || !strings.Contains(rt.lastURL, "/apps/com.example.app/anomalies") {
		t.Errorf("call = %s %s", rt.method, rt.lastURL)
	}
	// --since builds an activeBetween(...) filter.
	if !strings.Contains(rt.lastURL, "activeBetween") {
		t.Errorf("URL missing activeBetween filter: %q", rt.lastURL)
	}
	p := r.(Payload)
	if len(p.Anomalies) != 1 || p.Anomalies[0].MetricSet != "crashRateMetricSet" || p.Anomalies[0].Value != "0.08" {
		t.Errorf("anomalies = %+v", p.Anomalies)
	}
}

func TestRun_rawFilterOverridesSince(t *testing.T) {
	rc, rt, _ := newRC(t, `{"anomalies":[]}`)
	raw := `activeBetween("2026-01-01T00:00:00Z", UNBOUNDED)`
	// Both --since AND --filter are set: --filter must win, so the raw
	// (UNBOUNDED) predicate reaches the API and the --since-derived
	// activeBetween window is NOT used.
	if _, err := Run(rc, Input{Package: "com.example.app", Since: "7d", Filter: raw}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.lastURL, "UNBOUNDED") {
		t.Errorf("explicit --filter should override --since and reach the API: %q", rt.lastURL)
	}
}

func TestRun_emptyNotesFreshness(t *testing.T) {
	rc, _, stderr := newRC(t, `{"anomalies":[]}`)
	if _, err := Run(rc, Input{Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "no anomalies detected") {
		t.Errorf("empty result must note freshness; stderr = %q", stderr.String())
	}
}

func TestRun_noPackage_isUsageError(t *testing.T) {
	rc, _, _ := newRC(t, `{"anomalies":[]}`)
	_, err := Run(rc, Input{})
	var coder interface{ ExitCode() int }
	if err == nil || !errorsAs(err, &coder) || coder.ExitCode() != 2 {
		t.Fatalf("want usage error (exit 2), got %v", err)
	}
}

func errorsAs(err error, target *interface{ ExitCode() int }) bool {
	if c, ok := err.(interface{ ExitCode() int }); ok {
		*target = c
		return true
	}
	return false
}
