// Package details_test exercises the high-level details.Get entry
// point: open an Edit (read-only), fetch details.get + listings.get on
// the default language, discard the Edit. The transport NEVER sees a
// PUT or a :commit — `apps info` is read-only by construction.
package details_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

// infoRT routes the apps-info sequence: edits.insert, edits.details.get,
// edits.listings.get(defaultLanguage), edits.delete. Configurable status
// codes on details.get and listings.get exercise the error paths without
// re-declaring the transport. A PUT or a :commit fails the test — a
// read-only info command must never write.
type infoRT struct {
	t       *testing.T
	editID  string
	details string // body returned by /details (200 unless detailsCode set)
	listing string // body returned by /listings/{lang} (200 unless listingCode set)

	detailsCode int // 0 → 200
	listingCode int // 0 → 200

	mu    sync.Mutex
	calls []string
}

func (r *infoRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
		code := r.detailsCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.details), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/listings/"):
		code := r.listingCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.listing), nil
	}
	r.t.Fatalf("unexpected request (apps info is read-only): %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
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

// TestGet_happyPath asserts the full read-only sequence:
// insert → details.get → listings.get(defaultLanguage) → delete. The
// returned *Details surfaces defaultLanguage, title, contactEmail; the
// raw payload is the gplay envelope {"details":..,"listing":..} (an
// explicit exception to ADR-0003 — apps info combines two endpoints).
func TestGet_happyPath(t *testing.T) {
	detailsBody := `{"contactEmail":"hi@example.com","contactPhone":"+1","contactWebsite":"https://x","defaultLanguage":"en-US"}`
	listingBody := `{"language":"en-US","title":"MyApp","shortDescription":"hi","fullDescription":"world","video":""}`
	rt := &infoRT{
		t:       t,
		editID:  "edit-info",
		details: detailsBody,
		listing: listingBody,
	}
	hc := &http.Client{Transport: rt}

	d, raw, err := details.Get(context.Background(), hc, "com.example.app")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d == nil {
		t.Fatal("Get returned a nil *Details on happy path")
	}
	if d.DefaultLanguage != "en-US" {
		t.Errorf("DefaultLanguage = %q, want %q", d.DefaultLanguage, "en-US")
	}
	if d.Title != "MyApp" {
		t.Errorf("Title = %q, want %q", d.Title, "MyApp")
	}
	if d.ContactEmail != "hi@example.com" {
		t.Errorf("ContactEmail = %q, want %q", d.ContactEmail, "hi@example.com")
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-info/details",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-info/listings/en-US",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-info",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// The raw envelope is the gplay-shaped {"details":..,"listing":..}
	// — explicit exception to ADR-0003 because apps info merges two
	// endpoints. Each sub-object must be the upstream body verbatim so
	// jq/--output json consumers see the API field names unchanged.
	var env struct {
		Details json.RawMessage `json:"details"`
		Listing json.RawMessage `json:"listing"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("raw JSON is not the {details,listing} envelope: %v\nraw=%s", err, raw)
	}
	if strings.TrimSpace(string(env.Details)) != strings.TrimSpace(detailsBody) {
		t.Errorf("envelope.details = %s\nwant %s", env.Details, detailsBody)
	}
	if strings.TrimSpace(string(env.Listing)) != strings.TrimSpace(listingBody) {
		t.Errorf("envelope.listing = %s\nwant %s", env.Listing, listingBody)
	}
}

// TestGet_detailsGet403_mapsExit11_discardsEdit asserts that a 403 on
// details.get bubbles up as an *api.Error (ExitCode = 11) AND the
// opened Edit is still discarded by WithReadOnlyEdit's deferred
// cleanup. A dangling Edit on a permission failure would block the
// user's next publish for 24h.
func TestGet_detailsGet403_mapsExit11_discardsEdit(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-403",
		detailsCode: 403,
		details:     `{"error":{"code":403,"message":"The current user has insufficient permissions"}}`,
	}
	hc := &http.Client{Transport: rt}

	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11 (403 → authorization)", code)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error in the chain", err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("api.Error.StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Operation, "details.get") {
		t.Errorf("api.Error.Operation = %q, want it to mention details.get", apiErr.Operation)
	}
	// The Edit must still be discarded after a failed details.get.
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-403") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after 403; calls = %v", rt.calls)
	}
}

// TestGet_emptyDefaultLanguage_errorsBeforeListingsCall asserts the
// invariant the legacy GetDefaultLanguage already enforces (details.go
// "An empty defaultLanguage with a 2xx response is an API contract
// violation"): an empty defaultLanguage must fail fast in fetchDetails
// rather than silently flowing into a malformed /listings/ URL. The
// Edit is still discarded.
func TestGet_emptyDefaultLanguage_errorsBeforeListingsCall(t *testing.T) {
	rt := &infoRT{
		t:      t,
		editID: "edit-empty-lang",
		// defaultLanguage absent → parsed.DefaultLanguage = "".
		details: `{"contactEmail":"hi@example.com"}`,
		// Listing body that would be served IF the test ever reached
		// listings.get — we want the test to fail noisily if it does.
		listing: `{"language":"","title":"WOULDNT-REACH-HERE"}`,
	}
	hc := &http.Client{Transport: rt}

	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if err == nil {
		t.Fatal("expected an error for empty defaultLanguage, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error in the chain", err)
	}
	if !strings.Contains(apiErr.Operation, "details.get") {
		t.Errorf("api.Error.Operation = %q, want it to mention details.get", apiErr.Operation)
	}
	// listings.get must NOT have been called.
	for _, c := range rt.calls {
		if strings.Contains(c, "/listings/") {
			t.Errorf("listings.get was called after an empty defaultLanguage; calls = %v", rt.calls)
		}
	}
	// The Edit must still be discarded.
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-empty-lang") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after empty defaultLanguage; calls = %v", rt.calls)
	}
}

// TestGet_detailsGet404_mapsExit30 asserts a 404 on details.get surfaces
// as exit 30 (API 4xx other than auth/perms). Real-world trigger:
// service account never invited on the app, or wrong package name.
func TestGet_detailsGet404_mapsExit30(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-404",
		detailsCode: 404,
		details:     `{"error":{"code":404,"message":"Application not found"}}`,
	}
	hc := &http.Client{Transport: rt}

	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30 (404 → API 4xx)", code)
	}
}
