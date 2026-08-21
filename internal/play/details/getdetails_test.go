// Package details_test also exercises the high-level details.GetDetails
// entry point backing the `apps details` group: open a read-only Edit, do
// ONLY edits.details.get, discard the Edit. Unlike Get (which backs
// `apps view` and also hits listings.get), GetDetails touches a SINGLE
// endpoint, so its raw payload is the details.get body verbatim: a
// clean ADR-0003 pass-through with no gplay envelope. The transport
// FAILS on any PUT/PATCH/:commit AND on listings.get: a read of the App
// details resource must never mutate and must never reach a second
// endpoint.
package details_test

import (
	"context"
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

// getDetailsRT routes the apps-details read sequence: edits.insert,
// edits.details.get, edits.delete. A configurable status code on
// details.get exercises the error paths. Any other request: a PUT, a
// :commit, or listings.get: fails the test: GetDetails is read-only and
// single-endpoint by construction.
type getDetailsRT struct {
	t      *testing.T
	editID string
	body   string // body returned by /details (200 unless code set)
	code   int    // 0 → 200

	mu    sync.Mutex
	calls []string
}

func (r *getDetailsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
		code := r.code
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.body), nil
	}
	r.t.Fatalf("unexpected request (apps details is read-only, single-endpoint): %s %s", req.Method, req.URL)
	return nil, nil
}

// TestGetDetails_happyPath asserts the single-endpoint read-only sequence:
// insert → details.get → delete. The returned *AppDetails surfaces all
// four fields, and the raw payload is the details.get body verbatim (a
// clean ADR-0003 pass-through: no envelope, unlike Get).
func TestGetDetails_happyPath(t *testing.T) {
	body := `{"contactEmail":"hi@example.com","contactPhone":"+1 555 0100","contactWebsite":"https://x.example","defaultLanguage":"en-US"}`
	rt := &getDetailsRT{t: t, editID: "edit-details", body: body}
	hc := &http.Client{Transport: rt}

	d, raw, err := details.GetDetails(context.Background(), hc, "com.example.app")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if d == nil {
		t.Fatal("GetDetails returned a nil *AppDetails on happy path")
	}
	if d.DefaultLanguage != "en-US" {
		t.Errorf("DefaultLanguage = %q, want %q", d.DefaultLanguage, "en-US")
	}
	if d.ContactEmail != "hi@example.com" {
		t.Errorf("ContactEmail = %q, want %q", d.ContactEmail, "hi@example.com")
	}
	if d.ContactPhone != "+1 555 0100" {
		t.Errorf("ContactPhone = %q, want %q", d.ContactPhone, "+1 555 0100")
	}
	if d.ContactWebsite != "https://x.example" {
		t.Errorf("ContactWebsite = %q, want %q", d.ContactWebsite, "https://x.example")
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-details/details",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-details",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// The raw payload is the details.get body verbatim: no envelope.
	if strings.TrimSpace(string(raw)) != strings.TrimSpace(body) {
		t.Errorf("raw = %s\nwant details.get body verbatim = %s", raw, body)
	}
}

// TestGetDetails_get403_mapsExit11_discardsEdit asserts a 403 on
// details.get bubbles up as an *api.Error (ExitCode = 11) AND the opened
// Edit is still discarded: a dangling Edit on a permission failure would
// block the user's next publish for ~24h.
func TestGetDetails_get403_mapsExit11_discardsEdit(t *testing.T) {
	rt := &getDetailsRT{
		t:      t,
		editID: "edit-403",
		code:   403,
		body:   `{"error":{"code":403,"message":"The current user has insufficient permissions"}}`,
	}
	hc := &http.Client{Transport: rt}

	_, _, err := details.GetDetails(context.Background(), hc, "com.example.app")
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

// TestGetDetails_get404_mapsExit30 asserts a 404 on details.get surfaces
// as exit 30 (API 4xx other than auth/perms): e.g. an unknown package.
func TestGetDetails_get404_mapsExit30(t *testing.T) {
	rt := &getDetailsRT{
		t:      t,
		editID: "edit-404",
		code:   404,
		body:   `{"error":{"code":404,"message":"Application not found"}}`,
	}
	hc := &http.Client{Transport: rt}

	_, _, err := details.GetDetails(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30 (404 → API 4xx)", code)
	}
}

// patchBrokenBodyRT returns a 200 on the details PATCH with a body that
// errors on every Read (reusing details_test.brokenBody), so Patch's
// success-path io.ReadAll fails mid-stream.
type patchBrokenBodyRT struct{ t *testing.T }

func (r *patchBrokenBodyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPatch || !strings.HasSuffix(req.URL.Path, "/details") {
		r.t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       brokenBody{},
	}, nil
}

// TestPatch_bodyReadFailure_surfacesAsAPIError asserts that a read failure
// on Patch's success body is wrapped in *api.Error ("read response body")
// rather than silently flowing into json.Unmarshal as a decode error.
func TestPatch_bodyReadFailure_surfacesAsAPIError(t *testing.T) {
	hc := &http.Client{Transport: &patchBrokenBodyRT{t: t}}
	email := "hi@example.com"

	_, _, err := details.Patch(context.Background(), hc, "com.example.app", "edit-1", details.AppDetailsPatch{ContactEmail: &email})
	if err == nil {
		t.Fatal("expected an error for a broken response body, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error in the chain", err)
	}
	if !strings.Contains(apiErr.Message, "read response body") {
		t.Errorf("api.Error.Message = %q, want it to mention 'read response body'", apiErr.Message)
	}
	if !strings.Contains(apiErr.Operation, "details.patch") {
		t.Errorf("api.Error.Operation = %q, want it to mention details.patch", apiErr.Operation)
	}
}
