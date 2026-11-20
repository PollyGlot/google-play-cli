package details_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type getDetailsRT struct {
	t      *testing.T
	editID string
	body   string
	code   int

	fake *testkit.Fake
}

func (r *getDetailsRT) client() *http.Client {
	r.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/details"):
			return r.code, r.body, true
		}
		return 0, "", false
	})
	return &http.Client{Transport: r.fake}
}

func (r *getDetailsRT) calls() []string {
	var out []string
	for _, c := range r.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func TestGetDetails_happyPath(t *testing.T) {
	body := `{"contactEmail":"hi@example.com","contactPhone":"+1 555 0100","contactWebsite":"https://x.example","defaultLanguage":"en-US"}`
	rt := &getDetailsRT{t: t, editID: "edit-details", body: body}
	hc := rt.client()

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
	if len(rt.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls()), rt.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls()[i], want)
		}
	}

	if strings.TrimSpace(string(raw)) != strings.TrimSpace(body) {
		t.Errorf("raw = %s\nwant details.get body verbatim = %s", raw, body)
	}
}

func TestGetDetails_get403_mapsExit11_discardsEdit(t *testing.T) {
	rt := &getDetailsRT{
		t:      t,
		editID: "edit-403",
		code:   403,
		body:   `{"error":{"code":403,"message":"The current user has insufficient permissions"}}`,
	}
	hc := rt.client()

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
	for _, c := range rt.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-403") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after 403; calls = %v", rt.calls())
	}
}

func TestGetDetails_get404_mapsExit30(t *testing.T) {
	rt := &getDetailsRT{
		t:      t,
		editID: "edit-404",
		code:   404,
		body:   `{"error":{"code":404,"message":"Application not found"}}`,
	}
	hc := rt.client()

	_, _, err := details.GetDetails(context.Background(), hc, "com.example.app")
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30 (404 → API 4xx)", code)
	}
}

func TestPatch_bodyReadFailure_surfacesAsAPIError(t *testing.T) {
	hc := &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPatch || !strings.HasSuffix(req.URL.Path, "/details") {
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		resp := testkit.Response(200, "")
		resp.Body = brokenBody{}
		return resp, nil
	})}
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
