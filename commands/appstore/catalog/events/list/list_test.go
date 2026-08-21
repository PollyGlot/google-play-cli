package list_test

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
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	listcmd "github.com/PollyGlot/google-play-cli/commands/appstore/catalog/events/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// eventsRT answers the /token exchange and the recentupdateevents.list call,
// recording the API URL. Nothing here reaches the network.
type eventsRT struct {
	mu     sync.Mutex
	calls  []string
	apiURL string
	status int
	body   string
}

func (r *eventsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.apiURL = req.URL.String()
	if r.status != 0 {
		return jsonResp(r.status, r.body), nil
	}
	return jsonResp(200, eventsBody), nil
}

const eventsBody = `{
  "recentUpdateEvents": [
    {"playAppPackageName": "com.example.one", "eventTime": "2026-07-02T08:00:00Z", "updateType": "MODIFICATION"},
    {"playAppPackageName": "com.example.two", "eventTime": "2026-07-03T09:30:00Z", "updateType": "DELETION"}
  ],
  "nextPageToken": "tok-2"
}`

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stderr := &bytes.Buffer{}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, stderr
}

// query parses the recorded API URL's query string.
func query(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Query()
}

const (
	start = "2026-07-01T00:00:00Z"
	end   = "2026-07-08T00:00:00Z"
)

// TestRun_requestShape asserts the GET targets the recentUpdateEvents endpoint
// on the app store axis, carries both required time params, opens no Edit, and
// passes the envelope through verbatim on --output json (ADR-0003).
func TestRun_requestShape(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.alt", StartTime: start, EndTime: end})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstorecatalog/com.store.alt/recentUpdateEvents?") {
		t.Errorf("url %q is not the recentupdateevents.list endpoint", rt.apiURL)
	}
	if strings.Contains(rt.apiURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.apiURL)
	}
	q := query(t, rt.apiURL)
	if q.Get("startTime") != start {
		t.Errorf("startTime = %q, want %q", q.Get("startTime"), start)
	}
	if q.Get("endTime") != end {
		t.Errorf("endTime = %q, want %q", q.Get("endTime"), end)
	}
	// pageSize/pageToken are omitted when unset so the server applies its default.
	if q.Has("pageSize") || q.Has("pageToken") {
		t.Errorf("url %q must omit pageSize/pageToken when unset", rt.apiURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"nextPageToken"`) {
		t.Errorf("json %s should pass the ListRecentUpdateEventsResponse through verbatim", out.String())
	}
}

// TestRun_table asserts the human table carries one row per event with the
// package, time and MODIFICATION/DELETION type.
func TestRun_table(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.alt", StartTime: start, EndTime: end})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().Table(&out); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := out.String()
	for _, want := range []string{"PACKAGE", "TIME", "TYPE", "com.example.one", "2026-07-02T08:00:00Z", "MODIFICATION", "com.example.two", "DELETION"} {
		if !strings.Contains(got, want) {
			t.Errorf("table %q missing %q", got, want)
		}
	}
}

// TestRun_pagingParamsPropagate asserts --page-size and --page-token reach the
// query, so an incremental sync can walk the whole range.
func TestRun_pagingParamsPropagate(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	if _, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.alt", StartTime: start, EndTime: end, PageSize: 250, PageToken: "tok-1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	q := query(t, rt.apiURL)
	if q.Get("pageSize") != "250" {
		t.Errorf("pageSize = %q, want 250", q.Get("pageSize"))
	}
	if q.Get("pageToken") != "tok-1" {
		t.Errorf("pageToken = %q, want tok-1", q.Get("pageToken"))
	}
	// The time bounds must still ride along on a paged call.
	if q.Get("startTime") != start || q.Get("endTime") != end {
		t.Errorf("url %q must keep the same time range across pages", rt.apiURL)
	}
}

// TestRun_nextPageTokenNoted asserts the human path gets a stderr note carrying
// the next --page-token, so a table read never silently under-reports.
func TestRun_nextPageTokenNoted(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{}
	rc, stderr := newRC(t, rt)

	if _, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.alt", StartTime: start, EndTime: end}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "--page-token tok-2") {
		t.Errorf("stderr %q should carry the next page token", stderr.String())
	}
}

// TestRun_timeFlagValidation asserts every bad time input is CLI misuse (exit 2)
// caught before any HTTP call: the fail-fast contract for CI.
func TestRun_timeFlagValidation(t *testing.T) {
	cases := []struct {
		name       string
		start, end string
		wantIn     string
	}{
		{"missing start", "", end, "--start-time"},
		{"missing end", start, "", "--end-time"},
		{"malformed start", "2026-07-01", end, "RFC 3339"},
		{"malformed end", start, "next tuesday", "RFC 3339"},
		{"end before start", end, start, "must be after"},
		{"end equals start", start, start, "must be after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(appstorecmd.EnvStorePackage, "com.store.alt")
			rt := &eventsRT{}
			rc, _ := newRC(t, rt)

			_, err := listcmd.Run(rc, listcmd.Input{StartTime: tc.start, EndTime: tc.end})
			assertExit(t, err, 2)
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q must mention %q", err.Error(), tc.wantIn)
			}
			if len(rt.calls) != 0 {
				t.Errorf("must not reach the network; calls=%v", rt.calls)
			}
		})
	}
}

// TestRun_acceptsOffsetTimestamps asserts a non-UTC RFC 3339 offset is accepted
// and travels verbatim, so a caller need not convert to Z first.
func TestRun_acceptsOffsetTimestamps(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.alt")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	const offsetStart = "2026-07-01T02:00:00+02:00"
	if _, err := listcmd.Run(rc, listcmd.Input{StartTime: offsetStart, EndTime: end}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := query(t, rt.apiURL).Get("startTime"); got != offsetStart {
		t.Errorf("startTime = %q, want the offset timestamp verbatim %q", got, offsetStart)
	}
}

// TestRun_negativePageSize_exit2_noNetwork asserts a negative --page-size is CLI
// misuse caught before any HTTP call.
func TestRun_negativePageSize_exit2_noNetwork(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.alt")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	_, err := listcmd.Run(rc, listcmd.Input{StartTime: start, EndTime: end, PageSize: -1})
	assertExit(t, err, 2)
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_missingStorePackage_exit2_noNetwork asserts the shared app store
// package-name resolution applies here too, and fails before the network.
func TestRun_missingStorePackage_exit2_noNetwork(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	_, err := listcmd.Run(rc, listcmd.Input{StartTime: start, EndTime: end})
	assertExit(t, err, 2)
	if !strings.Contains(err.Error(), "--store-package") {
		t.Errorf("usage error %q must name --store-package", err.Error())
	}
	if len(rt.calls) != 0 {
		t.Errorf("must not reach the network; calls=%v", rt.calls)
	}
}

// TestRun_storePackageFromEnv asserts the CI path: the app store package name
// resolves from $GPLAY_APP_STORE_PACKAGE when the flag is omitted.
func TestRun_storePackageFromEnv(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "com.store.fromenv")
	rt := &eventsRT{}
	rc, _ := newRC(t, rt)

	if _, err := listcmd.Run(rc, listcmd.Input{StartTime: start, EndTime: end}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(rt.apiURL, "/appstorecatalog/com.store.fromenv/") {
		t.Errorf("url %q should use the app store package name from the environment", rt.apiURL)
	}
}

// TestRun_403_exit11 asserts a forbidden read maps to exit 11 with a refusal
// naming the app store package.
func TestRun_403_exit11(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{status: 403, body: `{"error":{"message":"The caller does not have permission"}}`}
	rc, _ := newRC(t, rt)

	_, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.alt", StartTime: start, EndTime: end})
	assertExit(t, err, 11)
	if !strings.Contains(err.Error(), "com.store.alt") {
		t.Errorf("403 refusal %q must name the app store package", err.Error())
	}
}

// TestRun_404_exit30 asserts an unknown app store package maps to the not-found
// exit code with an enrollment hint.
func TestRun_404_exit30(t *testing.T) {
	t.Setenv(appstorecmd.EnvStorePackage, "")
	rt := &eventsRT{status: 404, body: `{"error":{"message":"not found"}}`}
	rc, _ := newRC(t, rt)

	_, err := listcmd.Run(rc, listcmd.Input{StorePackage: "com.store.unknown", StartTime: start, EndTime: end})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "Catalog Export") {
		t.Errorf("404 hint %q should name the Catalog Export enrollment", err.Error())
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v has no ExitCode", err)
	}
	if got := c.ExitCode(); got != want {
		t.Errorf("exit = %d, want %d", got, want)
	}
}
