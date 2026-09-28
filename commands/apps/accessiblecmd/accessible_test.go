package accessiblecmd

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// lastCall returns the most recent API call the fake served, or a zero Call
// when none reached it.
func lastCall(fake *testkit.Fake) testkit.Call {
	calls := fake.Calls()
	if len(calls) == 0 {
		return testkit.Call{}
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
		"type": "service_account", "project_id": "p",
		"private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, body string) (*kernel.RunContext, *testkit.Fake, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fake := testkit.NewFake(testkit.Any(http.StatusOK, body))
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, fake, &stderr
}

const twoAppsBody = `{"apps":[{"name":"apps/com.example.a","packageName":"com.example.a","displayName":"Example A"},{"name":"apps/com.example.b","packageName":"com.example.b","displayName":"Example B"}]}`

// TestRun_listsAccessibleApps asserts Run hits the apps:search endpoint
// with a GET and parses the page into the Payload.
func TestRun_listsAccessibleApps(t *testing.T) {
	rc, fake, _ := newRC(t, twoAppsBody)
	r, err := Run(rc, Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := lastCall(fake); c.Method != http.MethodGet || !strings.Contains(c.URL, "/apps:search") {
		t.Errorf("call = %s %s, want GET .../apps:search", c.Method, c.URL)
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
	rc, fake, _ := newRC(t, `{"apps":[]}`)
	if _, err := Run(rc, Input{PageSize: 25, PageToken: "tok-1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if u := lastCall(fake).URL; !strings.Contains(u, "pageSize=25") || !strings.Contains(u, "pageToken=tok-1") {
		t.Errorf("url %q should carry pageSize and pageToken", u)
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
	rc, fake, _ := newRC(t, twoAppsBody)
	_, err := Run(rc, Input{PageSize: -1})
	if err == nil {
		t.Fatal("Run: expected usage error for negative --page-size, got nil")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse); err = %v", got, err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("no HTTP call should be made on a usage error; saw %q", calls[0].URL)
	}
}
