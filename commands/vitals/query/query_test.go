package query

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// queryRT covers the OAuth2 /token exchange and the metric-set :query POST.
type queryRT struct {
	t        *testing.T
	respBody string
	respCode int // 0 -> 200

	mu          sync.Mutex
	queryURL    string
	queryBody   string
	tokenScopes string
}

func (r *queryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenScopes = scopeClaim(r.t, req)
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.queryURL = req.URL.String()
	b, _ := io.ReadAll(req.Body)
	r.queryBody = string(b)
	code := r.respCode
	if code == 0 {
		code = 200
	}
	return jsonResp(code, r.respBody), nil
}

func jsonResp(code int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: h}
}

func scopeClaim(t *testing.T, req *http.Request) string {
	t.Helper()
	if err := req.ParseForm(); err != nil {
		return ""
	}
	a := req.PostFormValue("assertion")
	parts := strings.Split(a, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(payload, &claims)
	return claims.Scope
}

func signedSAJSON(t *testing.T) []byte {
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
	raw, _ := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	return raw
}

// newRC wires a reporting-scoped RunContext (mirroring kernel.WithScope) whose
// HTTP client routes through rt.
func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, &stdout, &stderr
}

const twoRowBody = `{"rows":[
  {"startTime":{"year":2026,"month":6,"day":1},"metrics":[{"metric":"crashRate","decimalValue":{"value":"0.0123"}}]},
  {"startTime":{"year":2026,"month":6,"day":2},"metrics":[{"metric":"crashRate","decimalValue":{"value":"0.0156"}}]}
]}`

func TestRun_crashrate_endToEnd(t *testing.T) {
	rt := &queryRT{t: t, respBody: twoRowBody}
	rc, _, stderr := newRC(t, rt)

	r, err := Run(rc, Input{MetricSet: "crashrate", Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The :query POST went to the crashrate resource under the reporting base.
	if want := "/apps/com.example.app/crashRateMetricSet:query"; !strings.HasSuffix(rt.queryURL, want) {
		t.Errorf("query URL = %q, want suffix %q", rt.queryURL, want)
	}
	if !strings.Contains(rt.queryURL, "playdeveloperreporting.googleapis.com") {
		t.Errorf("query did not hit the reporting host: %q", rt.queryURL)
	}
	// Default metric is the set's primary (crashRate); default period DAILY.
	if !strings.Contains(rt.queryBody, `"crashRate"`) {
		t.Errorf("body missing default metric crashRate: %s", rt.queryBody)
	}
	if !strings.Contains(rt.queryBody, `"DAILY"`) {
		t.Errorf("body missing default period DAILY: %s", rt.queryBody)
	}
	// Least privilege: the token was minted for the reporting scope only.
	if rt.tokenScopes != token.ReportingScope {
		t.Errorf("token scope = %q, want %q", rt.tokenScopes, token.ReportingScope)
	}

	p := r.(vitalscmd.Payload)
	if len(p.Timeline.Rows) != 2 {
		t.Fatalf("timeline rows = %d, want 2", len(p.Timeline.Rows))
	}
	if p.Timeline.Rows[0].Metrics["crashRate"] != "0.0123" {
		t.Errorf("row0 crashRate = %q", p.Timeline.Rows[0].Metrics["crashRate"])
	}

	// JSON render is the verbatim pass-through (rows preserved).
	var buf bytes.Buffer
	if err := p.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(buf.String(), `"crashRate"`) {
		t.Errorf("JSON pass-through lost the payload: %s", buf.String())
	}

	// Freshness note on stderr citing the freshest datapoint.
	if !strings.Contains(stderr.String(), "2026-06-02") {
		t.Errorf("stderr missing freshness note: %q", stderr.String())
	}
}

// TestRun_everyMetricSetReachable proves the generic command reaches every
// registered metric set (#260): each `vitals query <set>` issues its `:query`
// to that set's own REST resource.
func TestRun_everyMetricSetReachable(t *testing.T) {
	for _, ms := range vitals.MetricSets() {
		ms := ms
		t.Run(ms.Name, func(t *testing.T) {
			rt := &queryRT{t: t, respBody: `{"rows":[]}`}
			rc, _, _ := newRC(t, rt)
			if _, err := Run(rc, Input{MetricSet: ms.Name, Package: "com.example.app"}); err != nil {
				t.Fatalf("Run(%s): %v", ms.Name, err)
			}
			want := "/apps/com.example.app/" + ms.Resource + ":query"
			if !strings.HasSuffix(rt.queryURL, want) {
				t.Errorf("query %s URL = %q, want suffix %q", ms.Name, rt.queryURL, want)
			}
		})
	}
}

func TestRun_emptyWindow_warnsNotZero(t *testing.T) {
	rt := &queryRT{t: t, respBody: `{"rows":[]}`}
	rc, _, stderr := newRC(t, rt)
	if _, err := Run(rc, Input{MetricSet: "crashrate", Package: "com.example.app"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "not the same as zero") {
		t.Errorf("empty window must warn it is not zero; stderr = %q", stderr.String())
	}
}

func TestRun_unknownMetricSet_isUsageError(t *testing.T) {
	rt := &queryRT{t: t, respBody: `{}`}
	rc, _, _ := newRC(t, rt)
	_, err := Run(rc, Input{MetricSet: "bogusrate", Package: "com.example.app"})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
}

func TestRun_unknownMetric_isUsageError(t *testing.T) {
	rt := &queryRT{t: t, respBody: `{}`}
	rc, _, _ := newRC(t, rt)
	_, err := Run(rc, Input{MetricSet: "crashrate", Package: "com.example.app", Metrics: []string{"madeUpMetric"}})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
}

func TestRun_unknownPeriod_isUsageError(t *testing.T) {
	rt := &queryRT{t: t, respBody: `{}`}
	rc, _, _ := newRC(t, rt)
	_, err := Run(rc, Input{MetricSet: "crashrate", Package: "com.example.app", Period: "WEEKLY"})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
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
