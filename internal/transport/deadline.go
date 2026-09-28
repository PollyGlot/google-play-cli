package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// WithControlPlaneDeadline bounds every request inner carries by timeout,
// except a media transfer (IsMediaTransfer), which it passes through untouched.
//
// It exists for the upload commands: they drive one authenticated client
// through both the small Edit calls (edits.insert, tracks.update,
// edits.commit) and the multi-hundred-MB artifact transfer. A client-wide
// Timeout would either kill the transfer or leave the Edit calls unbounded, so
// a half-open connection during edits.commit hung the job until the runner
// killed it (docs/DESIGN.md §8). Deciding per request keeps the 60s promise for
// the control plane without touching the transfer.
//
// The deadline lives on the request context and is released only when the
// response body is closed, so it also covers reading the body, like
// http.Client.Timeout does.
func WithControlPlaneDeadline(inner http.RoundTripper, timeout time.Duration) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	if timeout <= 0 {
		return inner
	}
	return &deadlineTransport{inner: inner, timeout: timeout}
}

type deadlineTransport struct {
	inner   http.RoundTripper
	timeout time.Duration
}

func (d *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if IsMediaTransfer(req) {
		return d.inner.RoundTrip(req)
	}
	ctx, cancel := context.WithTimeout(req.Context(), d.timeout)
	resp, err := d.inner.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose releases the per-request deadline once the caller is done with
// the body, not when RoundTrip returns: canceling earlier would cut the body
// read short.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// IsMediaTransfer reports whether req moves media bytes: an upload that carries
// a body to a media endpoint (the Discovery `/upload/...` paths every upload
// URL is built from), or a download asking for the media itself (alt=media).
//
// A body-less request to an upload path stays control plane on purpose: the
// resumable protocol's initiate POST and its offset probe are small calls, and
// a hung one must fail fast rather than wait for the runner kill.
func IsMediaTransfer(req *http.Request) bool {
	if req.URL == nil {
		return false
	}
	if req.URL.Query().Get("alt") == "media" {
		return true
	}
	hasBody := req.Body != nil && req.Body != http.NoBody
	return hasBody && strings.HasPrefix(req.URL.Path, "/upload/")
}
