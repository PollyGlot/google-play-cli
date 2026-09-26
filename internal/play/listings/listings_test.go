// Package listings_test exercises the play-layer Listing operations
// against a fake transport. List/Get are the reads behind `gplay metadata
// list / pull`; Patch/Update/Delete are the per-locale writes behind
// `gplay metadata apply`. Each runs inside an already-open Edit, so the tests
// drive the bare operation, not the Edit lifecycle.
package listings_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// fakeWith answers every request with status and body.
func fakeWith(status int, body string) *testkit.Fake {
	return testkit.NewFake(testkit.Any(status, body))
}

// onlyCall returns the single request the Fake served, failing the test on
// any other count.
func onlyCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one request, got %d: %+v", len(calls), calls)
	}
	return calls[0]
}

// TestList_parsesEveryLocale_andReturnsRawBody asserts List GETs
// edits.listings.list, parses the {"listings":[...]} envelope into a
// Listing slice (one per locale), and hands back the raw body verbatim for
// the JSON pass-through.
func TestList_parsesEveryLocale_andReturnsRawBody(t *testing.T) {
	raw := `{"listings":[` +
		`{"language":"en-US","title":"My App","shortDescription":"short","fullDescription":"long desc","video":""},` +
		`{"language":"fr-FR","title":"Mon App","shortDescription":"","fullDescription":"desc longue","video":"https://youtu.be/x"}` +
		`],"kind":"androidpublisher#listingsListResponse"}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := listings.List(context.Background(), hc, "com.example.app", "edit-123")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/listings"
	c := onlyCall(t, transport)
	if c.Method != http.MethodGet || c.Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.Path, wantPath)
	}
	if len(got) != 2 {
		t.Fatalf("got %d listings, want 2: %+v", len(got), got)
	}
	if got[0].Language != "en-US" || got[0].Title != "My App" || got[0].ShortDescription != "short" {
		t.Errorf("listing[0] = %+v, want en-US My App", got[0])
	}
	if got[1].Language != "fr-FR" || got[1].Video != "https://youtu.be/x" {
		t.Errorf("listing[1] = %+v, want fr-FR with video", got[1])
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim envelope %s", gotRaw, raw)
	}
}

// TestGet_parsesListing_andPathCarriesLanguage asserts Get GETs
// edits.listings.get on a single locale (the language is the last path
// segment) and parses the Listing.
func TestGet_parsesListing_andPathCarriesLanguage(t *testing.T) {
	raw := `{"language":"en-US","title":"My App","shortDescription":"short","fullDescription":"long desc","video":""}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := listings.Get(context.Background(), hc, "com.example.app", "edit-123", "en-US")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/listings/en-US"
	c := onlyCall(t, transport)
	if c.Method != http.MethodGet || c.Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.Path, wantPath)
	}
	if got.Language != "en-US" || got.Title != "My App" || got.FullDescription != "long desc" {
		t.Errorf("listing = %+v, want en-US My App", got)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim %s", gotRaw, raw)
	}
}

// TestPatch_sendsBodyVerbatim_andReturnsRaw asserts Patch PATCHes
// edits.listings.patch on the locale, transmits the caller-built body
// verbatim, and returns the raw response body (the patched Listing).
func TestPatch_sendsBodyVerbatim_andReturnsRaw(t *testing.T) {
	reqBody := `{"language":"en-US","title":"New Title","fullDescription":"new full"}`
	respBody := `{"language":"en-US","title":"New Title","shortDescription":"","fullDescription":"new full","video":""}`
	transport := fakeWith(200, respBody)
	hc := &http.Client{Transport: transport}

	gotRaw, err := listings.Patch(context.Background(), hc, "com.example.app", "edit-123", "en-US", []byte(reqBody))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/listings/en-US"
	c := onlyCall(t, transport)
	if c.Method != http.MethodPatch || c.Path != wantPath {
		t.Errorf("request = %s %s, want PATCH %s", c.Method, c.Path, wantPath)
	}
	if string(c.Body) != reqBody {
		t.Errorf("request body = %s, want verbatim %s", c.Body, reqBody)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(respBody) {
		t.Errorf("response body = %s, want verbatim %s", gotRaw, respBody)
	}
}

// TestUpdate_sendsPUT_withFullBody_andReturnsRaw asserts Update PUTs
// edits.listings.update on the locale path (the only Listings call that can
// create a language absent online, #561), transmits the complete
// caller-built body verbatim, and returns the raw response body.
func TestUpdate_sendsPUT_withFullBody_andReturnsRaw(t *testing.T) {
	reqBody := `{"fullDescription":"Lange Beschreibung","language":"de-DE","shortDescription":"Kurz","title":"Meine App"}`
	respBody := `{"language":"de-DE","title":"Meine App","shortDescription":"Kurz","fullDescription":"Lange Beschreibung","video":""}`
	transport := fakeWith(200, respBody)
	hc := &http.Client{Transport: transport}

	gotRaw, err := listings.Update(context.Background(), hc, "com.example.app", "edit-123", "de-DE", []byte(reqBody))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/listings/de-DE"
	c := onlyCall(t, transport)
	if c.Method != http.MethodPut || c.Path != wantPath {
		t.Errorf("request = %s %s, want PUT %s", c.Method, c.Path, wantPath)
	}
	if string(c.Body) != reqBody {
		t.Errorf("request body = %s, want verbatim %s", c.Body, reqBody)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(respBody) {
		t.Errorf("response body = %s, want verbatim %s", gotRaw, respBody)
	}
}

// TestUpdate_apiError_carriesUpdateOperation asserts a failed PUT surfaces
// as an *api.Error named listings.update (not listings.patch), so the error
// envelope names the call that actually ran.
func TestUpdate_apiError_carriesUpdateOperation(t *testing.T) {
	transport := fakeWith(404, `{"error":{"code":404,"message":"Listing for language 'xx-XX' not found."}}`)
	hc := &http.Client{Transport: transport}

	_, err := listings.Update(context.Background(), hc, "com.example.app", "edit-123", "xx-XX", []byte(`{}`))
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.Operation != "listings.update" || apiErr.StatusCode != 404 {
		t.Errorf("api.Error = {Operation:%q Status:%d}, want {listings.update 404}", apiErr.Operation, apiErr.StatusCode)
	}
}

// TestDelete_issuesDelete_onLocalePath asserts Delete DELETEs
// edits.listings.delete on the locale path (drops the whole Listing).
func TestDelete_issuesDelete_onLocalePath(t *testing.T) {
	transport := fakeWith(204, ``)
	hc := &http.Client{Transport: transport}

	if err := listings.Delete(context.Background(), hc, "com.example.app", "edit-123", "en-US"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/listings/en-US"
	c := onlyCall(t, transport)
	if c.Method != http.MethodDelete || c.Path != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", c.Method, c.Path, wantPath)
	}
}

// TestList_apiError_isWrappedWithStatus asserts a non-2xx listings.list
// response surfaces as an *api.Error carrying the HTTP status so the gplay
// exit-code taxonomy maps it (403 -> 11, 404 -> 30, ...).
func TestList_apiError_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(403, `{"error":{"code":403,"message":"The caller does not have permission"}}`)
	hc := &http.Client{Transport: transport}

	_, _, err := listings.List(context.Background(), hc, "com.example.app", "edit-123")
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

// TestGet_notFound_isWrappedWithStatus asserts a 404 on listings.get (no
// Listing for the locale) surfaces as an *api.Error carrying 404.
func TestGet_notFound_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(404, `{"error":{"code":404,"message":"Listing not found"}}`)
	hc := &http.Client{Transport: transport}

	_, _, err := listings.Get(context.Background(), hc, "com.example.app", "edit-123", "de-DE")
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
