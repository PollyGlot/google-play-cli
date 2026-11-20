package tracks_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
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

func TestList_parsesEveryTrack_andReturnsRawBody(t *testing.T) {
	raw := `{"tracks":[` +
		`{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]},` +
		`{"track":"internal","releases":[]},` +
		`{"track":"qa-closed","releases":[{"name":"99","status":"completed","versionCodes":["99"]}]}` +
		`]}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := tracks.List(context.Background(), hc, "com.example.app", "edit-123")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/tracks"
	c := onlyCall(t, transport)
	if c.Method != http.MethodGet || c.Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", c.Method, c.Path, wantPath)
	}
	if len(got) != 3 {
		t.Fatalf("got %d tracks, want 3: %+v", len(got), got)
	}
	if got[0].Track != "production" || len(got[0].Releases) != 1 || got[0].Releases[0].Name != "142" {
		t.Errorf("track[0] = %+v, want production with release 142", got[0])
	}
	if got[1].Track != "internal" || len(got[1].Releases) != 0 {
		t.Errorf("track[1] = %+v, want internal with no releases", got[1])
	}
	if got[2].Track != "qa-closed" {
		t.Errorf("track[2].Track = %q, want qa-closed", got[2].Track)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim envelope %s", gotRaw, raw)
	}
}

func TestList_apiError_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(403, `{"error":{"code":403,"message":"The caller does not have permission"}}`)
	hc := &http.Client{Transport: transport}

	_, _, err := tracks.List(context.Background(), hc, "com.example.app", "edit-123")
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

func TestCreate_postsClosedTestingConfig_andReturnsRawBody(t *testing.T) {
	raw := `{"track":"qa-team","releases":[]}`
	transport := fakeWith(200, raw)
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := tracks.Create(context.Background(), hc, "com.example.app", "edit-123", "qa-team", tracks.FormFactorDefault)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/tracks"
	c := onlyCall(t, transport)
	if c.Method != http.MethodPost || c.Path != wantPath {
		t.Errorf("request = %s %s, want POST %s", c.Method, c.Path, wantPath)
	}
	body := string(c.Body)
	for _, want := range []string{`"track":"qa-team"`, `"type":"CLOSED_TESTING"`, `"formFactor":"DEFAULT"`} {
		if !strings.Contains(body, want) {
			t.Errorf("request body = %s, want it to contain %s", body, want)
		}
	}
	if got.Track != "qa-team" {
		t.Errorf("Track = %q, want qa-team", got.Track)
	}
	if strings.TrimSpace(string(gotRaw)) != strings.TrimSpace(raw) {
		t.Errorf("raw body = %s, want verbatim response %s", gotRaw, raw)
	}
}

func TestCreate_apiError_isWrappedWithStatus(t *testing.T) {
	transport := fakeWith(400, `{"error":{"code":400,"message":"Track already exists.","errors":[{"reason":"badRequest"}]}}`)
	hc := &http.Client{Transport: transport}

	_, _, err := tracks.Create(context.Background(), hc, "com.example.app", "edit-123", "qa-team", tracks.FormFactorDefault)
	if err == nil {
		t.Fatal("expected an error on a 400 response, got nil")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if len(apiErr.Reasons) == 0 || apiErr.Reasons[0] != "badRequest" {
		t.Errorf("Reasons = %v, want [badRequest] surfaced from the envelope", apiErr.Reasons)
	}
}
