package testers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/testers"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func fakeWith(status int, body string) *testkit.Fake {
	return testkit.NewFake(testkit.Any(status, body))
}

func onlyCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one request, got %d: %+v", len(calls), calls)
	}
	return calls[0]
}

func TestGet_parsesGoogleGroups_andReturnsRawBody(t *testing.T) {
	raw := `{"googleGroups":["qa@googlegroups.com","beta@googlegroups.com"]}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := testers.Get(context.Background(), hc, "com.example.app", "edit-123", "qa-team")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/testers/qa-team"
	c := onlyCall(t, transport)
	if c.Method != http.MethodGet || c.Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.Path, wantPath)
	}
	if len(got.GoogleGroups) != 2 || got.GoogleGroups[0] != "qa@googlegroups.com" || got.GoogleGroups[1] != "beta@googlegroups.com" {
		t.Errorf("GoogleGroups = %v, want [qa@googlegroups.com beta@googlegroups.com]", got.GoogleGroups)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim resource %s", gotRaw, raw)
	}
}

func TestGet_apiError_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(404, `{"error":{"code":404,"message":"Track not found"}}`)
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

func TestUpdate_putsFullReplacement_andParsesResponse(t *testing.T) {
	resp := `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`
	transport := fakeWith(200, resp)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := testers.Update(context.Background(), hc, "com.example.app", "edit-123", "qa-team",
		[]string{"a@googlegroups.com", "b@googlegroups.com"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/testers/qa-team"
	c := onlyCall(t, transport)
	if c.Method != http.MethodPut || c.Path != wantPath {
		t.Errorf("request = %s %s, want PUT %s", c.Method, c.Path, wantPath)
	}
	wantBody := `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`
	if strings.TrimSpace(string(c.Body)) != wantBody {
		t.Errorf("request body = %s, want %s", c.Body, wantBody)
	}
	if len(got.GoogleGroups) != 2 {
		t.Errorf("GoogleGroups = %v, want two groups", got.GoogleGroups)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(resp) {
		t.Errorf("raw body = %s, want verbatim resource %s", gotRaw, resp)
	}
}

func TestUpdate_clear_putsEmptyArray(t *testing.T) {
	transport := fakeWith(200, `{"googleGroups":[]}`)
	hc := &http.Client{Transport: transport}

	_, _, err := testers.Update(context.Background(), hc, "com.example.app", "edit-123", "qa-team", nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	body := string(onlyCall(t, transport).Body)
	if !strings.Contains(body, `"googleGroups":[]`) {
		t.Errorf("request body = %s, want it to contain \"googleGroups\":[]", body)
	}
	if strings.Contains(body, "null") {
		t.Errorf("request body = %s, want an empty array, not null", body)
	}
}

func TestUpdate_apiError_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(403, `{"error":{"code":403,"message":"The caller does not have permission"}}`)
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
