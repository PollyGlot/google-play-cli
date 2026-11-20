package transport_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

func hangOrAnswer(hang bool) http.RoundTripper {
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if hang {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		resp := testkit.Response(http.StatusOK, `{"ok":true}`)
		resp.Request = req
		return resp, nil
	})
}

func TestControlPlaneDeadline_cutsHungControlPlaneOnly(t *testing.T) {
	hc := &http.Client{Transport: transport.WithControlPlaneDeadline(hangOrAnswer(true), 50*time.Millisecond)}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/p/edits/e1:commit", nil)
	start := time.Now()
	_, err := hc.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("hung request returned after %v", d)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	media, _ := http.NewRequestWithContext(ctx, http.MethodPut, "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/x", strings.NewReader("bytes"))
	start = time.Now()
	_, err = hc.Do(media)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("media transfer cut after %v (%v): it must not carry the control-plane bound", time.Since(start), err)
	}
}

func TestControlPlaneDeadline_bodyReadableAfterRoundTrip(t *testing.T) {
	hc := &http.Client{Transport: transport.WithControlPlaneDeadline(hangOrAnswer(false), time.Minute)}
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
		{http.MethodPost, "https://h/upload/androidpublisher/v3/a?uploadType=resumable", false, false},
		{http.MethodPut, "https://h/upload/androidpublisher/v3/a?upload_id=1", false, false},
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

func mediaServer(body func(*http.Request) io.ReadCloser) http.RoundTripper {
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp := testkit.Response(http.StatusOK, "")
		resp.Body = body(req)
		resp.Request = req
		return resp, nil
	})
}

func download(t *testing.T, rt http.RoundTripper) ([]byte, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://storage.googleapis.com/storage/v1/b/bkt/o/x.csv?alt=media", nil)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

func TestControlPlaneDeadline_downloadBoundedByInactivityNotTotal(t *testing.T) {
	const bound = 100 * time.Millisecond
	want := []byte("0123456789")
	rt := transport.WithControlPlaneDeadline(mediaServer(func(req *http.Request) io.ReadCloser {
		return testkit.TrickleBody(req.Context(), want, 1, 30*time.Millisecond)
	}), bound)

	start := time.Now()
	got, err := download(t, rt)
	if err != nil {
		t.Fatalf("slow but progressing download failed after %v: %v", time.Since(start), err)
	}
	if string(got) != string(want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if d := time.Since(start); d < 2*bound {
		t.Fatalf("download took %v, under 2x the bound: the test proves nothing", d)
	}
}

func TestControlPlaneDeadline_stalledDownloadFails(t *testing.T) {
	const bound = 50 * time.Millisecond
	rt := transport.WithControlPlaneDeadline(mediaServer(func(req *http.Request) io.ReadCloser {
		return testkit.StalledBody(req.Context(), []byte("head"))
	}), bound)

	start := time.Now()
	_, err := download(t, rt)
	var idle *transport.IdleTimeoutError
	if !errors.As(err, &idle) || idle.Idle != bound {
		t.Fatalf("err = %v (%T), want *transport.IdleTimeoutError{Idle: %v}", err, err, bound)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false, want true")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("stalled download returned after %v", d)
	}
}

func TestControlPlaneDeadline_downloadWithoutHeadersFails(t *testing.T) {
	rt := transport.WithControlPlaneDeadline(hangOrAnswer(true), 50*time.Millisecond)
	_, err := download(t, rt)
	var idle *transport.IdleTimeoutError
	if !errors.As(err, &idle) {
		t.Fatalf("err = %v (%T), want *transport.IdleTimeoutError", err, err)
	}
}
