// Package countryavailability_test exercises the low-level Get against a
// simulated transport: a single edits.countryavailability.get inside an
// Edit the caller has already opened (mirroring tracks.Get / testers.Get).
// It asserts the exact upstream URL (countryAvailability/{track}), the
// parsed fields, the verbatim raw body, and the *api.Error mapping.
package countryavailability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
)

// caRT answers a single GET on the countryAvailability/{track} path with
// a configurable status + body, recording the requested path so the test
// can assert the exact upstream URL.
type caRT struct {
	t    *testing.T
	body string
	code int // 0 → 200

	mu       sync.Mutex
	lastPath string
	calls    int
}

func (r *caRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastPath = req.URL.Path
	if req.Method != http.MethodGet {
		r.t.Fatalf("unexpected method %s (countryavailability.Get is read-only)", req.Method)
	}
	code := r.code
	if code == 0 {
		code = 200
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

// TestGet_happyPath asserts Get issues a GET to the correct
// countryAvailability/{track} URL, parses syncWithProduction /
// restOfWorld / countries[], and returns the body verbatim.
func TestGet_happyPath(t *testing.T) {
	body := `{"syncWithProduction":false,"restOfWorld":true,"countries":[{"countryCode":"US"},{"countryCode":"GB"},{"countryCode":"FR"}]}`
	rt := &caRT{t: t, body: body}
	hc := &http.Client{Transport: rt}

	ca, raw, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "production")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ca == nil {
		t.Fatal("Get returned nil *TrackCountryAvailability on happy path")
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/countryAvailability/production"
	if rt.lastPath != wantPath {
		t.Errorf("GET path = %q, want %q", rt.lastPath, wantPath)
	}
	if ca.SyncWithProduction {
		t.Errorf("SyncWithProduction = true, want false")
	}
	if !ca.RestOfWorld {
		t.Errorf("RestOfWorld = false, want true")
	}
	gotCodes := make([]string, 0, len(ca.Countries))
	for _, c := range ca.Countries {
		gotCodes = append(gotCodes, c.CountryCode)
	}
	want := []string{"US", "GB", "FR"}
	if strings.Join(gotCodes, ",") != strings.Join(want, ",") {
		t.Errorf("Countries = %v, want %v", gotCodes, want)
	}

	if strings.TrimSpace(string(raw)) != strings.TrimSpace(body) {
		t.Errorf("raw = %s\nwant body verbatim = %s", raw, body)
	}
}

// TestGet_403_mapsExit11 asserts a 403 surfaces as *api.Error (exit 11).
func TestGet_403_mapsExit11(t *testing.T) {
	rt := &caRT{t: t, code: 403, body: `{"error":{"code":403,"message":"insufficient permissions"}}`}
	hc := &http.Client{Transport: rt}

	_, _, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "production")
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error in the chain", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

// TestGet_404_mapsExit30 asserts a 404 (unknown track or package) maps to
// exit 30.
func TestGet_404_mapsExit30(t *testing.T) {
	rt := &caRT{t: t, code: 404, body: `{"error":{"code":404,"message":"not found"}}`}
	hc := &http.Client{Transport: rt}

	_, _, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "no-such-track")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
}

// brokenBody errors on the first Read so io.ReadAll fails mid-stream — a
// truncated 2xx response must surface as an I/O failure, not be silently
// passed to json.Unmarshal as if the body were complete.
type brokenBody struct{}

func (brokenBody) Read(_ []byte) (int, error) { return 0, errors.New("simulated read error") }
func (brokenBody) Close() error               { return nil }

// brokenBodyRT returns a 200 on countryAvailability.get with a body that
// errors on every Read.
type brokenBodyRT struct{ t *testing.T }

func (r *brokenBodyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		r.t.Fatalf("unexpected method %s", req.Method)
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       brokenBody{},
	}, nil
}

// TestGet_bodyReadFailure_surfacesAsAPIError asserts that a network read
// failure on the success body is wrapped in *api.Error rather than
// silently flowing into json.Unmarshal as if the body were complete.
func TestGet_bodyReadFailure_surfacesAsAPIError(t *testing.T) {
	hc := &http.Client{Transport: &brokenBodyRT{t: t}}

	_, _, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "production")
	if err == nil {
		t.Fatal("expected an error for a broken response body, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error in the chain", err)
	}
	if !strings.Contains(apiErr.Message, "read response") {
		t.Errorf("api.Error.Message = %q, want it to mention 'read response'", apiErr.Message)
	}
}
