// Package countryavailability_test exercises the low-level Get against a
// simulated transport: a single edits.countryavailability.get inside an
// Edit the caller has already opened (mirroring tracks.Get / testers.Get).
// It asserts the exact upstream URL (countryAvailability/{track}), the
// parsed fields, the verbatim raw body, and the *api.Error mapping.
package countryavailability_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// newCA answers GETs with a configurable status + body (code 0 means 200).
// Any other method finds no responder, so the read-only Get fails loudly if
// it ever writes.
func newCA(code int, body string) (*testkit.Fake, *http.Client) {
	f := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		return code, body, c.Method == http.MethodGet
	})
	return f, &http.Client{Transport: f}
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
	f, hc := newCA(0, body)

	ca, raw, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "production")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ca == nil {
		t.Fatal("Get returned nil *TrackCountryAvailability on happy path")
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-1/countryAvailability/production"
	if calls := f.Calls(); len(calls) != 1 || calls[0].Path != wantPath {
		t.Errorf("calls = %+v, want one GET %q", calls, wantPath)
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
	_, hc := newCA(403, `{"error":{"code":403,"message":"insufficient permissions"}}`)

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
	_, hc := newCA(404, `{"error":{"code":404,"message":"not found"}}`)

	_, _, err := countryavailability.Get(context.Background(), hc, "com.example.app", "edit-1", "no-such-track")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
}

// brokenBody errors on the first Read so io.ReadAll fails mid-stream: a
// truncated 2xx response must surface as an I/O failure, not be silently
// passed to json.Unmarshal as if the body were complete.
type brokenBody struct{}

func (brokenBody) Read(_ []byte) (int, error) { return 0, errors.New("simulated read error") }
func (brokenBody) Close() error               { return nil }

// TestGet_bodyReadFailure_surfacesAsAPIError asserts that a network read
// failure on the success body is wrapped in *api.Error rather than
// silently flowing into json.Unmarshal as if the body were complete, and
// exits 50: the answer arrived, then the network failed.
func TestGet_bodyReadFailure_surfacesAsAPIError(t *testing.T) {
	hc := &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("unexpected method %s", req.Method)
		}
		resp := testkit.Response(http.StatusOK, "")
		resp.Body = brokenBody{}
		return resp, nil
	})}

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
	if code := exitCodeOf(t, err); code != 50 {
		t.Errorf("ExitCode() = %d, want 50 (a body cut mid-read is a network failure)", code)
	}
}
