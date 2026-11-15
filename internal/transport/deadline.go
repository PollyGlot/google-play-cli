package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// WithControlPlaneDeadline bounds every request inner carries by timeout,
// except a media transfer (IsMediaTransfer): a media upload passes through
// untouched, and a media download (alt=media) is bounded by timeout of
// inactivity instead of total duration (see idleRoundTrip).
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
	if isMediaDownload(req) {
		return d.idleRoundTrip(req)
	}
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

// IdleTimeoutError is the failure of a media download that received no byte
// for Idle: the headers did not arrive, or the body stopped moving. It wraps
// context.DeadlineExceeded, so it reads as the timeout it is.
type IdleTimeoutError struct{ Idle time.Duration }

func (e *IdleTimeoutError) Error() string {
	return "download stalled: no data received for " + e.Idle.String()
}

func (e *IdleTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// idleRoundTrip bounds a media download by inactivity, not by total duration:
// a large report export on a slow link outlasts any fixed total, while a
// connection that delivers nothing for d.timeout is as dead as a hung control
// plane call. The clock starts with the request (waiting for the headers counts
// as inactivity) and restarts on every body read that returns bytes.
func (d *deadlineTransport) idleRoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	stalled := &IdleTimeoutError{Idle: d.timeout}
	timer := time.AfterFunc(d.timeout, func() { cancel(stalled) })
	resp, err := d.inner.RoundTrip(req.WithContext(ctx))
	if err != nil {
		timer.Stop()
		cancel(nil)
		if errors.Is(context.Cause(ctx), stalled) {
			return nil, stalled
		}
		return nil, err
	}
	resp.Body = &idleBody{ReadCloser: resp.Body, ctx: ctx, cancel: cancel, timer: timer, stalled: stalled}
	return resp, nil
}

// idleBody re-arms the download's inactivity clock on every read that moves
// bytes, and reports a read the clock cut short as the IdleTimeoutError rather
// than whatever the inner transport makes of the cancellation.
type idleBody struct {
	io.ReadCloser
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	stalled *IdleTimeoutError
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.stalled.Idle)
	}
	if err != nil && !errors.Is(err, io.EOF) && errors.Is(context.Cause(b.ctx), b.stalled) {
		err = b.stalled
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel(nil)
	return err
}

// isMediaDownload reports whether req asks for the media bytes themselves
// (alt=media): the half of IsMediaTransfer that idleRoundTrip bounds.
func isMediaDownload(req *http.Request) bool {
	return req.URL != nil && req.URL.Query().Get("alt") == "media"
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
	if isMediaDownload(req) {
		return true
	}
	hasBody := req.Body != nil && req.Body != http.NoBody
	return hasBody && strings.HasPrefix(req.URL.Path, "/upload/")
}
