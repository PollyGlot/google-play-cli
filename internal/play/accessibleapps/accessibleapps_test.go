package accessibleapps_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const pageBody = `{"apps":[{"name":"apps/com.example.a","packageName":"com.example.a","displayName":"Example A"},{"name":"apps/com.example.b","packageName":"com.example.b","displayName":"Example B"}],"nextPageToken":"tok-2"}`

// TestSearch_hitsReportingHost_parsesPage asserts Search targets the
// Reporting service's apps:search endpoint (not the androidpublisher host),
// makes a GET, parses the page, and returns the body verbatim.
func TestSearch_hitsReportingHost_parsesPage(t *testing.T) {
	var gotURL, gotMethod string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotMethod = r.Method
		return resp(200, pageBody), nil
	})
	hc := &http.Client{Transport: rt}

	sr, raw, err := accessibleapps.Search(context.Background(), hc, 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotURL, "https://playdeveloperreporting.googleapis.com/") {
		t.Errorf("url %q should hit the Play Developer Reporting host", gotURL)
	}
	if !strings.Contains(gotURL, "/apps:search") {
		t.Errorf("url %q should target the apps:search method", gotURL)
	}
	if len(sr.Apps) != 2 || sr.Apps[0].PackageName != "com.example.a" || sr.Apps[1].DisplayName != "Example B" {
		t.Errorf("parsed apps = %+v, want the two page entries", sr.Apps)
	}
	if sr.NextPageToken != "tok-2" {
		t.Errorf("NextPageToken = %q, want tok-2", sr.NextPageToken)
	}
	if strings.TrimSpace(string(raw)) != pageBody {
		t.Errorf("raw = %s, want verbatim pass-through", raw)
	}
}

// TestSearch_pagingParams asserts pageSize (>0) and pageToken are sent as
// query params, and pageSize <= 0 is omitted so the server applies its
// default.
func TestSearch_pagingParams(t *testing.T) {
	var gotURL string
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return resp(200, `{"apps":[]}`), nil
	})
	hc := &http.Client{Transport: rt}

	if _, _, err := accessibleapps.Search(context.Background(), hc, 25, "tok-1"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(gotURL, "pageSize=25") {
		t.Errorf("url %q should carry pageSize=25", gotURL)
	}
	if !strings.Contains(gotURL, "pageToken=tok-1") {
		t.Errorf("url %q should carry pageToken=tok-1", gotURL)
	}

	// pageSize <= 0 must be omitted.
	if _, _, err := accessibleapps.Search(context.Background(), hc, 0, ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(gotURL, "pageSize") {
		t.Errorf("url %q should omit pageSize when <= 0", gotURL)
	}
}

// TestSearch_apiError_carriesStatus asserts a non-2xx becomes an *api.Error
// with the status, so the exit-code taxonomy can map it (403 → exit 11).
func TestSearch_apiError_carriesStatus(t *testing.T) {
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return resp(http.StatusForbidden, `{"error":{"code":403,"message":"no reporting access"}}`), nil
	})
	hc := &http.Client{Transport: rt}

	_, _, err := accessibleapps.Search(context.Background(), hc, 0, "")
	if err == nil {
		t.Fatal("Search: expected error on 403, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T, want *api.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

// TestSearch_emptyResult asserts an empty page decodes cleanly to zero Apps
// and an empty token (a valid, common state — the credential simply sees no
// Apps on the Reporting side).
func TestSearch_emptyResult(t *testing.T) {
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return resp(200, `{}`), nil
	})
	hc := &http.Client{Transport: rt}

	sr, _, err := accessibleapps.Search(context.Background(), hc, 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(sr.Apps) != 0 || sr.NextPageToken != "" {
		t.Errorf("empty page = %+v, want zero apps and no token", sr)
	}
}
