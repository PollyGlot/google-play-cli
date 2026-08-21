// Package tracks_test exercises the play-layer track operations against a
// fake transport. List is the cross-track read used by `gplay tracks
// list`: one GET on edits.tracks.list inside an already-open Edit,
// returning every configured track plus the raw body for the ADR-0003
// JSON pass-through.
package tracks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// rt is a minimal RoundTripper that returns a canned response for the
// tracks.list GET (and the tracks.create POST) and records the request
// line (plus the request body, for write ops) for assertion.
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

// TestList_parsesEveryTrack_andReturnsRawBody asserts List GETs
// edits.tracks.list, parses the {"tracks":[...]} envelope into a Track
// slice (every configured track, standard and custom), and hands back
// the raw body verbatim for the JSON pass-through.
func TestList_parsesEveryTrack_andReturnsRawBody(t *testing.T) {
	raw := `{"tracks":[` +
		`{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]},` +
		`{"track":"internal","releases":[]},` +
		`{"track":"qa-closed","releases":[{"name":"99","status":"completed","versionCodes":["99"]}]}` +
		`]}`
	transport := &rt{t: t, body: raw}
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := tracks.List(context.Background(), hc, "com.example.app", "edit-123")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/tracks"
	if transport.gotMethod != http.MethodGet || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want GET %s", transport.gotMethod, transport.gotPath, wantPath)
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

// TestList_apiError_isWrappedWithStatus asserts a non-2xx tracks.list
// response surfaces as an *api.Error carrying the HTTP status so the
// gplay exit-code taxonomy maps it (403 -> 11, 404 -> 30, ...).
func TestList_apiError_isWrappedWithStatus(t *testing.T) {
	transport := &rt{t: t, status: 403, body: `{"error":{"code":403,"message":"The caller does not have permission"}}`}
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

// TestCreate_postsClosedTestingConfig_andReturnsRawBody asserts Create
// POSTs edits.tracks.create with a TrackConfig that hardcodes the only
// supported type (CLOSED_TESTING) and the DEFAULT form factor, carrying
// the caller's track name, and hands back the parsed Track plus the raw
// body verbatim for the ADR-0003 JSON pass-through.
func TestCreate_postsClosedTestingConfig_andReturnsRawBody(t *testing.T) {
	raw := `{"track":"qa-team","releases":[]}`
	transport := &rt{t: t, body: raw}
	hc := &http.Client{Transport: transport}

	got, gotRaw, err := tracks.Create(context.Background(), hc, "com.example.app", "edit-123", "qa-team", tracks.FormFactorDefault)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantPath := "/androidpublisher/v3/applications/com.example.app/edits/edit-123/tracks"
	if transport.gotMethod != http.MethodPost || transport.gotPath != wantPath {
		t.Errorf("request = %s %s, want POST %s", transport.gotMethod, transport.gotPath, wantPath)
	}
	body := string(transport.gotBody)
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

// TestCreate_apiError_isWrappedWithStatus asserts a non-2xx
// tracks.create response (e.g. creating a track that already exists)
// surfaces as an *api.Error carrying the HTTP status so the exit-code
// taxonomy maps it (400 -> 30 via StatusToExitCode), with the envelope
// reason preserved.
func TestCreate_apiError_isWrappedWithStatus(t *testing.T) {
	transport := &rt{t: t, status: 400, body: `{"error":{"code":400,"message":"Track already exists.","errors":[{"reason":"badRequest"}]}}`}
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
