package details_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type infoRT struct {
	t       *testing.T
	editID  string
	details string
	listing string
	icon    string

	detailsCode int
	listingCode int
	iconCode    int

	fake *testkit.Fake
}

func (r *infoRT) client() *http.Client {
	r.fake = testkit.NewFake(r.respond)
	return &http.Client{Transport: r.fake}
}

func (r *infoRT) respond(c testkit.Call) (int, string, bool) {
	switch {
	case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
		return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID), true
	case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
		return 204, "", true
	case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/details"):
		return r.detailsCode, r.details, true
	case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/icon"):
		body := r.icon
		if body == "" {
			body = `{"images":[]}`
		}
		return r.iconCode, body, true
	case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
		return r.listingCode, r.listing, true
	}
	return 0, "", false
}

func (r *infoRT) calls() []string {
	var out []string
	for _, c := range r.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
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

func TestGet_happyPath(t *testing.T) {
	detailsBody := `{"contactEmail":"hi@example.com","contactPhone":"+1","contactWebsite":"https://x","defaultLanguage":"en-US"}`
	listingBody := `{"language":"en-US","title":"MyApp","shortDescription":"hi","fullDescription":"world","video":""}`
	rt := &infoRT{
		t:       t,
		editID:  "edit-info",
		details: detailsBody,
		listing: listingBody,
	}
	hc := rt.client()

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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-info/listings/en-US/icon",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-info",
	}
	if len(rt.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls()), rt.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls()[i], want)
		}
	}

	var env struct {
		Details json.RawMessage `json:"details"`
		Listing json.RawMessage `json:"listing"`
		Icon    json.RawMessage `json:"icon"`
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
	if len(env.Icon) != 0 {
		t.Errorf("envelope.icon should be omitted when the icon slot is empty, got %s", env.Icon)
	}
	if d.Icon != nil {
		t.Errorf("d.Icon = %+v, want nil (empty icon slot)", d.Icon)
	}
}

func TestGet_iconPresent_addsIconKey(t *testing.T) {
	detailsBody := `{"contactEmail":"hi@example.com","defaultLanguage":"en-US"}`
	listingBody := `{"language":"en-US","title":"MyApp"}`
	iconBody := `{"images":[{"id":"ic1","url":"https://play.example/icon.png","sha1":"deadbeef","sha256":"ICON_SHA_256"}]}`
	rt := &infoRT{
		t:       t,
		editID:  "edit-icon",
		details: detailsBody,
		listing: listingBody,
		icon:    iconBody,
	}
	hc := rt.client()

	d, raw, err := details.Get(context.Background(), hc, "com.example.app")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Icon == nil {
		t.Fatal("d.Icon = nil, want a populated *Icon")
	}
	if d.Icon.Sha256 != "ICON_SHA_256" {
		t.Errorf("d.Icon.Sha256 = %q, want ICON_SHA_256", d.Icon.Sha256)
	}
	if d.Icon.URL != "https://play.example/icon.png" {
		t.Errorf("d.Icon.URL = %q, want the preview url verbatim", d.Icon.URL)
	}

	var env struct {
		Icon *struct {
			URL    string `json:"url"`
			Sha256 string `json:"sha256"`
		} `json:"icon"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nraw=%s", err, raw)
	}
	if env.Icon == nil {
		t.Fatalf("envelope.icon missing, want {url,sha256}; raw=%s", raw)
	}
	if env.Icon.Sha256 != "ICON_SHA_256" || env.Icon.URL != "https://play.example/icon.png" {
		t.Errorf("envelope.icon = %+v, want verbatim url+sha256", env.Icon)
	}
	inserts := 0
	for _, c := range rt.calls() {
		if strings.HasSuffix(c, "/edits") && strings.HasPrefix(c, "POST ") {
			inserts++
		}
	}
	if inserts != 1 {
		t.Errorf("edits.insert count = %d, want exactly 1 (same Edit reused)", inserts)
	}
}

func TestGet_iconRead403_mapsExit11(t *testing.T) {
	rt := &infoRT{
		t:        t,
		editID:   "edit-icon-403",
		details:  `{"contactEmail":"hi@example.com","defaultLanguage":"en-US"}`,
		listing:  `{"language":"en-US","title":"MyApp"}`,
		iconCode: 403,
		icon:     `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	hc := rt.client()
	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11 (403 on icon read)", code)
	}
}

func TestGet_iconRead404_mapsExit30(t *testing.T) {
	rt := &infoRT{
		t:        t,
		editID:   "edit-icon-404",
		details:  `{"contactEmail":"hi@example.com","defaultLanguage":"en-US"}`,
		listing:  `{"language":"en-US","title":"MyApp"}`,
		iconCode: 404,
		icon:     `{"error":{"code":404,"message":"not found"}}`,
	}
	hc := rt.client()
	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30 (404 on icon read)", code)
	}
}

func TestGet_detailsGet403_mapsExit11_discardsEdit(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-403",
		detailsCode: 403,
		details:     `{"error":{"code":403,"message":"The current user has insufficient permissions"}}`,
	}
	hc := rt.client()

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
	sawDelete := false
	for _, c := range rt.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-403") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after 403; calls = %v", rt.calls())
	}
}

func TestGet_emptyDefaultLanguage_errorsBeforeListingsCall(t *testing.T) {
	rt := &infoRT{
		t:       t,
		editID:  "edit-empty-lang",
		details: `{"contactEmail":"hi@example.com"}`,
		listing: `{"language":"","title":"WOULDNT-REACH-HERE"}`,
	}
	hc := rt.client()

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
	for _, c := range rt.calls() {
		if strings.Contains(c, "/listings/") {
			t.Errorf("listings.get was called after an empty defaultLanguage; calls = %v", rt.calls())
		}
	}
	sawDelete := false
	for _, c := range rt.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-empty-lang") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after empty defaultLanguage; calls = %v", rt.calls())
	}
}

type brokenBody struct{}

func (brokenBody) Read(_ []byte) (int, error) { return 0, errors.New("simulated read error") }
func (brokenBody) Close() error               { return nil }

type detailsBodyReadErrorRT struct {
	t      *testing.T
	editID string

	mu    sync.Mutex
	lines []string
}

func (r *detailsBodyReadErrorRT) client() *http.Client {
	return &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.lines = append(r.lines, req.Method+" "+req.URL.Path)
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
			return testkit.Response(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
		case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
			return testkit.Response(204, ""), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
			resp := testkit.Response(200, "")
			resp.Body = brokenBody{}
			return resp, nil
		}
		r.t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		return nil, fmt.Errorf("unexpected request %s %s", req.Method, req.URL)
	})}
}

func (r *detailsBodyReadErrorRT) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func TestGet_bodyReadFailure_surfacesAsAPIError(t *testing.T) {
	rt := &detailsBodyReadErrorRT{t: t, editID: "edit-readerr"}
	hc := rt.client()

	_, _, err := details.Get(context.Background(), hc, "com.example.app")
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
	if !strings.Contains(apiErr.Operation, "details.get") {
		t.Errorf("api.Error.Operation = %q, want it to mention details.get", apiErr.Operation)
	}
	if code := exitCodeOf(t, err); code != 50 {
		t.Errorf("ExitCode() = %d, want 50 (a body cut mid-read is a network failure)", code)
	}
	sawDelete := false
	for _, c := range rt.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-readerr") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after read failure; calls = %v", rt.calls())
	}
}

func TestGet_detailsGet404_mapsExit30(t *testing.T) {
	rt := &infoRT{
		t:           t,
		editID:      "edit-404",
		detailsCode: 404,
		details:     `{"error":{"code":404,"message":"Application not found"}}`,
	}
	hc := rt.client()

	_, _, err := details.Get(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30 (404 → API 4xx)", code)
	}
}
