// Package testers_test exercises the play-layer testers operations against
// a fake transport. Get reads the authorized audience of a track
// (edits.testers.get); Update replaces it wholesale (edits.testers.update,
// a PUT). Both run inside an already-open Edit and return the parsed
// resource plus the raw body for the ADR-0003 JSON pass-through.
package testers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/testers"
)

// rt is a minimal RoundTripper that returns a canned response for the
// testers.get GET (and the testers.update PUT) and records the request
// line — plus the request body, for write ops — for assertion.
type rt struct {
	t      *testing.T
	status int
	body   string

	gotPath   string
	gotMethod string
	gotBody   []byte
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.gotPath = req.URL.Path
	r.gotMethod = req.Method
	if req.Body != nil {
		r.gotBody, _ = io.ReadAll(req.Body)
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
}

// TestGet_parsesGoogleGroups_andReturnsRawBody asserts Get GETs
// edits.testers.get, parses the {"googleGroups":[...]} resource, and hands
// back the raw body verbatim for the JSON pass-through.
func TestGet_parsesGoogleGroups_andReturnsRawBody(t *testing.T) {
	raw := `{"googleGroups":["qa@googlegroups.com","beta@googlegroups.com"]}`
	transport := &rt{t: t, body: raw}
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := testers.Get(context.Background(), hc, "com.example.app", "edit-123", "qa-team")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/testers/qa-team"
	if transport.gotMethod != http.MethodGet || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want GET %s", transport.gotMethod, transport.gotPath, wantPath)
	}
	if len(got.GoogleGroups) != 2 || got.GoogleGroups[0] != "qa@googlegroups.com" || got.GoogleGroups[1] != "beta@googlegroups.com" {
		t.Errorf("GoogleGroups = %v, want [qa@googlegroups.com beta@googlegroups.com]", got.GoogleGroups)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim resource %s", gotRaw, raw)
	}
}

// TestGet_apiError_isWrappedWithStatus asserts a non-2xx testers.get
// response surfaces as an *api.Error carrying the HTTP status so the
// gplay exit-code taxonomy maps it (404 -> 30, ...).
func TestGet_apiError_isWrappedWithStatus(t *testing.T) {
	transport := &rt{t: t, status: 404, body: `{"error":{"code":404,"message":"Track not found"}}`}
	hc := &http.Client{Transport: transport}

	_, _, err := testers.Get(context.Background(), hc, "com.example.app", "edit-123", "qa-team")
	if err == nil {
		t.Fatal("expected an error on a 404 response, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// TestUpdate_putsFullReplacement_andParsesResponse asserts Update PUTs
// edits.testers.update, sends the full {"googleGroups":[...]} replacement
// body, parses the response, and hands back the raw body verbatim.
func TestUpdate_putsFullReplacement_andParsesResponse(t *testing.T) {
	resp := `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`
	transport := &rt{t: t, body: resp}
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := testers.Update(context.Background(), hc, "com.example.app", "edit-123", "qa-team",
		[]string{"a@googlegroups.com", "b@googlegroups.com"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/testers/qa-team"
	if transport.gotMethod != http.MethodPut || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want PUT %s", transport.gotMethod, transport.gotPath, wantPath)
	}
	wantBody := `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`
	if strings.TrimSpace(string(transport.gotBody)) != wantBody {
		t.Errorf("request body = %s, want %s", transport.gotBody, wantBody)
	}
	if len(got.GoogleGroups) != 2 {
		t.Errorf("GoogleGroups = %v, want two groups", got.GoogleGroups)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(resp) {
		t.Errorf("raw body = %s, want verbatim resource %s", gotRaw, resp)
	}
}

// TestUpdate_clear_putsEmptyArray asserts that Update with a nil group
// list sends an explicit empty array ("googleGroups":[]) — the --clear
// semantics — NOT a null, so the API clears the audience rather than
// rejecting a malformed body.
func TestUpdate_clear_putsEmptyArray(t *testing.T) {
	transport := &rt{t: t, body: `{"googleGroups":[]}`}
	hc := &http.Client{Transport: transport}

	_, _, err := testers.Update(context.Background(), hc, "com.example.app", "edit-123", "qa-team", nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	body := string(transport.gotBody)
	if !strings.Contains(body, `"googleGroups":[]`) {
		t.Errorf("request body = %s, want it to contain \"googleGroups\":[]", body)
	}
	if strings.Contains(body, "null") {
		t.Errorf("request body = %s, want an empty array, not null", body)
	}
}

// TestUpdate_apiError_isWrappedWithStatus asserts a non-2xx
// testers.update response surfaces as an *api.Error carrying the HTTP
// status so the gplay exit-code taxonomy maps it (403 -> 11, ...).
func TestUpdate_apiError_isWrappedWithStatus(t *testing.T) {
	transport := &rt{t: t, status: 403, body: `{"error":{"code":403,"message":"The caller does not have permission"}}`}
	hc := &http.Client{Transport: transport}

	_, _, err := testers.Update(context.Background(), hc, "com.example.app", "edit-123", "qa-team",
		[]string{"a@googlegroups.com"})
	if err == nil {
		t.Fatal("expected an error on a 403 response, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}
