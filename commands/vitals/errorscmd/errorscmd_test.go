package errorscmd

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

type errorsRT struct {
	mu      sync.Mutex
	body    string
	lastURL string
	method  string
}

func (r *errorsRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{
		"type": "service_account", "project_id": "p",
		"private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	return raw
}

func newRC(t *testing.T, body string) (*kernel.RunContext, *errorsRT, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rt := &errorsRT{body: body}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, rt, &stderr
}

func TestRunCounts_queriesErrorCountSet(t *testing.T) {
	rc, rt, _ := newRC(t, `{"rows":[{"startTime":{"year":2026,"month":6,"day":1},"metrics":[{"metric":"errorReportCount","decimalValue":{"value":"5"}}]}]}`)
	if _, err := runCounts(rc, countsInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runCounts: %v", err)
	}
	if rt.method != http.MethodPost || !strings.HasSuffix(rt.lastURL, "/apps/com.example.app/errorCountMetricSet:query") {
		t.Errorf("counts call = %s %s", rt.method, rt.lastURL)
	}
}

func TestRunIssues_searchesAndWarnsAboutMappings(t *testing.T) {
	body := `{"errorIssues":[{"type":"CRASH","cause":"NullPointerException","location":"A.b","errorReportCount":"7","distinctUsers":"5","lastErrorReportTime":"2026-06-15T10:00:00Z"}]}`
	rc, rt, stderr := newRC(t, body)
	r, err := runIssues(rc, issuesInput{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	if rt.method != http.MethodGet || !strings.Contains(rt.lastURL, "/errorIssues:search") {
		t.Errorf("issues call = %s %s", rt.method, rt.lastURL)
	}
	p := r.(issuesPayload)
	if len(p.Issues) != 1 || p.Issues[0].Cause != "NullPointerException" {
		t.Errorf("issues = %+v", p.Issues)
	}
	if !strings.Contains(stderr.String(), "#250") {
		t.Errorf("issues with frames must note mappings (#250); stderr = %q", stderr.String())
	}
}

func TestRunIssues_emptyWarnsNotZero(t *testing.T) {
	rc, _, stderr := newRC(t, `{"errorIssues":[]}`)
	if _, err := runIssues(rc, issuesInput{Package: "com.example.app"}); err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	if !strings.Contains(stderr.String(), "not the same as zero") {
		t.Errorf("empty issues must warn; stderr = %q", stderr.String())
	}
}

func TestRunReports_searchesAndKeepsFrames(t *testing.T) {
	body := `{"errorReports":[{"type":"CRASH","eventTime":"2026-06-15T09:00:00Z","reportText":"java.lang.NullPointerException\n\tat a.b.c(Unknown Source)","appVersion":{"versionCode":"123"},"osVersion":{"apiLevel":"31"},"deviceModel":{"marketingName":"Pixel 7"}}]}`
	rc, rt, stderr := newRC(t, body)
	r, err := runReports(rc, reportsInput{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("runReports: %v", err)
	}
	if rt.method != http.MethodGet || !strings.Contains(rt.lastURL, "/errorReports:search") {
		t.Errorf("reports call = %s %s", rt.method, rt.lastURL)
	}
	p := r.(reportsPayload)
	if len(p.Reports) != 1 || p.Reports[0].AppVersion != "123" || p.Reports[0].Device != "Pixel 7" {
		t.Errorf("reports = %+v", p.Reports)
	}

	// Table REPORT column shows the (obfuscated) first frame line.
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("table: %v", err)
	}
	if !strings.Contains(buf.String(), "NullPointerException") {
		t.Errorf("report table lost the frame: %s", buf.String())
	}
	if !strings.Contains(stderr.String(), "#250") {
		t.Errorf("reports must note mappings (#250); stderr = %q", stderr.String())
	}
}
