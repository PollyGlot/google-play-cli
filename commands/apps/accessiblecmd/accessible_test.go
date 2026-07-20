package accessiblecmd

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
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type searchRT struct {
	mu      sync.Mutex
	body    string
	lastURL string
	method  string
}

func (r *searchRT) RoundTrip(req *http.Request) (*http.Response, error) {
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

func newRC(t *testing.T, body string) (*kernel.RunContext, *searchRT, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rt := &searchRT{body: body}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, rt, &stderr
}

const twoAppsBody = `{"apps":[{"name":"apps/com.example.a","packageName":"com.example.a","displayName":"Example A"},{"name":"apps/com.example.b","packageName":"com.example.b","displayName":"Example B"}]}`

// TestRun_listsAccessibleApps asserts Run hits the apps:search endpoint
// with a GET and parses the page into the Payload.
func TestRun_listsAccessibleApps(t *testing.T) {
	rc, rt, _ := newRC(t, twoAppsBody)
	r, err := Run(rc, Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.method != http.MethodGet || !strings.Contains(rt.lastURL, "/apps:search") {
		t.Errorf("call = %s %s, want GET .../apps:search", rt.method, rt.lastURL)
	}
	p := r.(Payload)
	if len(p.Apps) != 2 || p.Apps[0].PackageName != "com.example.a" || p.Apps[1].DisplayName != "Example B" {
		t.Errorf("Payload.Apps = %+v, want the two entries", p.Apps)
	}
}

// TestRun_jsonPassthrough asserts the raw body is preserved verbatim for
// the ADR-0003 --output json pass-through.
func TestRun_jsonPassthrough(t *testing.T) {
	rc, _, _ := newRC(t, twoAppsBody)
	r, err := Run(rc, Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(string(r.(Payload).Raw)) != twoAppsBody {
		t.Errorf("Raw = %s, want verbatim body", r.(Payload).Raw)
	}
}

// TestRun_paginationParams asserts --page-size/--page-token reach the URL.
func TestRun_paginationParams(t *testing.T) {
	rc, rt, _ := newRC(t, `{"apps":[]}`)
	if _, err := Run(rc, Input{PageSize: 25, PageToken: "tok-1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.lastURL, "pageSize=25") || !strings.Contains(rt.lastURL, "pageToken=tok-1") {
		t.Errorf("url %q should carry pageSize and pageToken", rt.lastURL)
	}
}

// TestRun_nextPageTokenNote asserts a page carrying nextPageToken emits a
// stderr note telling the operator how to fetch the next page.
func TestRun_nextPageTokenNote(t *testing.T) {
	rc, _, stderr := newRC(t, `{"apps":[{"packageName":"com.example.a"}],"nextPageToken":"tok-next"}`)
	if _, err := Run(rc, Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "tok-next") || !strings.Contains(stderr.String(), "--page-token") {
		t.Errorf("stderr %q should carry the next --page-token note", stderr.String())
	}
}

// TestRun_negativePageSize_usageError asserts a negative --page-size is CLI
// misuse (exit 2) rejected before any HTTP call.
func TestRun_negativePageSize_usageError(t *testing.T) {
	rc, rt, _ := newRC(t, twoAppsBody)
	_, err := Run(rc, Input{PageSize: -1})
	if err == nil {
		t.Fatal("Run: expected usage error for negative --page-size, got nil")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err = %v", got, err)
	}
	if rt.lastURL != "" {
		t.Errorf("no HTTP call should be made on a usage error; saw %q", rt.lastURL)
	}
}
