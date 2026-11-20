package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

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

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

type IdleTimeoutError struct{ Idle time.Duration }

func (e *IdleTimeoutError) Error() string {
	return "download stalled: no data received for " + e.Idle.String()
}

func (e *IdleTimeoutError) Unwrap() error { return context.DeadlineExceeded }

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

func isMediaDownload(req *http.Request) bool {
	return req.URL != nil && req.URL.Query().Get("alt") == "media"
}

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
