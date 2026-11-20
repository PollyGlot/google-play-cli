package countryavailability_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type urlRT struct{ url, verb string }

func (r *urlRT) client() *http.Client {
	return &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		r.url = req.URL.String()
		r.verb = req.Method
		return testkit.Response(http.StatusOK, `{"syncWithProduction":true,"restOfWorld":false,"countries":[]}`), nil
	})}
}

func TestGet_absoluteURLUnchanged(t *testing.T) {
	rt := &urlRT{}
	if _, _, err := countryavailability.Get(context.Background(), rt.client(), "com.example.app", "edit-1", "production"); err != nil {
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

func TestGet_emptyTrackFailsBeforeTheWire(t *testing.T) {
	rt := &urlRT{}
	_, _, err := countryavailability.Get(context.Background(), rt.client(), "com.example.app", "edit-1", "")
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
