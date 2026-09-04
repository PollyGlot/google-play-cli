// This file is the migration proof for #516: countryavailability was the first
// package to take its verb and URL from internal/apiregistry instead of a local
// literal. The pre-existing countryavailability_test.go is untouched and still
// asserts the exact path; what is added here is the part that file never
// checked, the absolute URL (host included) and the behaviour on an empty
// parameter, so the next migration batches have a template to copy.
package countryavailability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
)

// urlRT records the whole request URL, not just its path.
type urlRT struct{ url, verb string }

func (r *urlRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.url = req.URL.String()
	r.verb = req.Method
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"syncWithProduction":true,"restOfWorld":false,"countries":[]}`)),
	}, nil
}

// TestGet_absoluteURLUnchanged pins the full URL and verb the resolver
// produces, so a change in the Discovery snapshot or in the resolver's
// reconstruction cannot silently move this call to another endpoint.
func TestGet_absoluteURLUnchanged(t *testing.T) {
	rt := &urlRT{}
	if _, _, err := countryavailability.Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "edit-1", "production"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/edit-1/countryAvailability/production"
	if rt.url != want {
		t.Errorf("URL = %q, want %q", rt.url, want)
	}
	if rt.verb != http.MethodGet {
		t.Errorf("verb = %q, want GET", rt.verb)
	}
}

// TestGet_emptyTrackFailsBeforeTheWire asserts an empty path parameter is
// refused locally, as an *api.Error, rather than sent as a truncated URL.
func TestGet_emptyTrackFailsBeforeTheWire(t *testing.T) {
	rt := &urlRT{}
	_, _, err := countryavailability.Get(context.Background(), &http.Client{Transport: rt}, "com.example.app", "edit-1", "")
	if err == nil {
		t.Fatal("Get with an empty track succeeded, want an error")
	}
	if rt.url != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", rt.url)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"track"`) {
		t.Errorf("message = %q, want it to name the track parameter", apiErr.Message)
	}
}
