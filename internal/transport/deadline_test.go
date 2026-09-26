package transport_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// hangOrAnswer blocks a request until its context is done when hang is set,
// else answers with a body that is read after RoundTrip returns.
type hangOrAnswer struct{ hang bool }

func (h hangOrAnswer) RoundTrip(req *http.Request) (*http.Response, error) {
	if h.hang {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: req}, nil
}

// A hung control-plane request fails on the bound instead of hanging, while a
// media transfer on the same transport is left alone.
func TestControlPlaneDeadline_cutsHungControlPlaneOnly(t *testing.T) {
	hc := &http.Client{Transport: transport.WithControlPlaneDeadline(hangOrAnswer{hang: true}, 50*time.Millisecond)}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/p/edits/e1:commit", nil)
	start := time.Now()
	_, err := hc.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("hung request returned after %v", d)
	}

	// The media PUT is not bounded: prove it by canceling it ourselves after
	// the control-plane bound would long have fired.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	media, _ := http.NewRequestWithContext(ctx, http.MethodPut, "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/x", strings.NewReader("bytes"))
	start = time.Now()
	_, err = hc.Do(media)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("media transfer cut after %v (%v): it must not carry the control-plane bound", time.Since(start), err)
	}
}

// The bound stays armed until the body is closed, not just until headers: the
// body must still be readable after RoundTrip returns.
func TestControlPlaneDeadline_bodyReadableAfterRoundTrip(t *testing.T) {
	hc := &http.Client{Transport: transport.WithControlPlaneDeadline(hangOrAnswer{}, time.Minute)}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/p/edits/e1", nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(b) != `{"ok":true}` {
		t.Fatalf("body = %q, %v", b, err)
	}
}

func TestIsMediaTransfer(t *testing.T) {
	cases := []struct {
		method, url string
		body        bool
		want        bool
	}{
		{http.MethodPut, "https://h/upload/androidpublisher/v3/a?upload_id=1", true, true},
		{http.MethodPost, "https://h/upload/androidpublisher/v3/a?uploadType=media", true, true},
		{http.MethodPost, "https://h/upload/androidpublisher/v3/a?uploadType=resumable", false, false}, // initiate
		{http.MethodPut, "https://h/upload/androidpublisher/v3/a?upload_id=1", false, false},           // probe
		{http.MethodGet, "https://h/androidpublisher/v3/a:download?alt=media", false, true},
		{http.MethodPost, "https://h/androidpublisher/v3/applications/p/edits", true, false},
	}
	for _, c := range cases {
		var body io.Reader
		if c.body {
			body = strings.NewReader("x")
		}
		req, _ := http.NewRequestWithContext(t.Context(), c.method, c.url, body)
		if got := transport.IsMediaTransfer(req); got != c.want {
			t.Errorf("IsMediaTransfer(%s %s, body=%v) = %v, want %v", c.method, c.url, c.body, got, c.want)
		}
	}
}
