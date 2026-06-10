package transport

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 30 * time.Second
)

// RetryOptions configures WithRetry.
type RetryOptions struct {
	// MaxRetries is the number of *additional* attempts after the first
	// (0 disables retry — WithRetry is then a no-op passthrough).
	MaxRetries int
	// Timeout bounds each individual attempt (0 = no per-attempt deadline).
	// Retry makes the global --timeout a per-attempt bound rather than a single
	// per-request one, so a hung attempt is abandoned and retried.
	Timeout time.Duration
	// BaseDelay / MaxDelay bound the exponential backoff (defaults 500ms / 30s).
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

// WithRetry wraps inner so transport-level failures, HTTP 5xx, and HTTP 429
// (honoring Retry-After) are retried up to opts.MaxRetries times with
// exponential backoff plus jitter. Other 4xx (auth, validation) are never
// retried — retrying them just wastes time. `edits.commit` is never retried:
// it is the one operation where a duplicate could double-publish (its exit-60
// conflict classification already guides the caller). Request bodies are
// recreated per attempt via Request.GetBody, so a retried upload re-sends from a
// fresh reader. MaxRetries == 0 returns inner unchanged.
func WithRetry(inner http.RoundTripper, opts RetryOptions) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	if opts.MaxRetries <= 0 {
		return inner
	}
	rt := &retryTransport{
		inner:      inner,
		maxRetries: opts.MaxRetries,
		timeout:    opts.Timeout,
		baseDelay:  opts.BaseDelay,
		maxDelay:   opts.MaxDelay,
		jitter:     fullJitter,
		sleep:      ctxSleep,
	}
	if rt.baseDelay <= 0 {
		rt.baseDelay = defaultRetryBaseDelay
	}
	if rt.maxDelay <= 0 {
		rt.maxDelay = defaultRetryMaxDelay
	}
	return rt
}

type retryTransport struct {
	inner      http.RoundTripper
	maxRetries int
	timeout    time.Duration
	baseDelay  time.Duration
	maxDelay   time.Duration
	// jitter and sleep are seams: production uses fullJitter / ctxSleep; tests
	// inject deterministic, non-blocking variants.
	jitter func(time.Duration) time.Duration
	sleep  func(ctx context.Context, d time.Duration) bool
}

func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	excluded := isNonRetryable(req)
	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; ; attempt++ {
		attemptReq, berr := cloneWithFreshBody(req)
		if berr != nil {
			return nil, berr
		}
		// A throwaway client gives each attempt its own deadline and the
		// correct "cancel when the body is closed" semantics for the returned
		// response (the caller reads resp.Body after RoundTrip returns).
		attemptClient := &http.Client{Transport: rt.inner, Timeout: rt.timeout}
		resp, err = attemptClient.Do(attemptReq)

		if excluded || attempt >= rt.maxRetries || !retryable(resp, err) {
			return resp, err
		}
		// Drain + close the discarded response so the connection can be reused.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if !rt.sleep(req.Context(), rt.backoff(attempt, resp)) {
			// Context canceled during backoff — return the last result instead
			// of spinning through doomed attempts.
			return resp, err
		}
	}
}

// retryable reports whether a (resp, err) outcome warrants another attempt:
// any transport error, an HTTP 429, or any 5xx. A 2xx or a non-429 4xx is
// terminal.
func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

// isNonRetryable excludes the operations that must never auto-retry. Today that
// is edits.commit (path suffix ":commit"): a duplicate commit could
// double-publish.
func isNonRetryable(req *http.Request) bool {
	return strings.HasSuffix(req.URL.Path, ":commit")
}

// cloneWithFreshBody clones req with a body rebuilt from GetBody, so every
// attempt sends the full payload from the start. NewRequestWithContext sets
// GetBody automatically for the in-memory body types gplay uses (bytes/strings
// readers), and the upload paths set it explicitly — so a replayable body is
// always available for a request that has one.
func cloneWithFreshBody(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}
	if req.GetBody == nil {
		// No replay source: keep the original body (correct for the first
		// attempt; a retry of such a request would send an empty body, but
		// every gplay request with a body provides GetBody).
		clone.Body = req.Body
		return clone, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

// backoff returns the delay before the next attempt. A 429 with a Retry-After
// header is honored verbatim; otherwise it is exponential (base * 2^attempt,
// capped at MaxDelay) with jitter.
func (rt *retryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			return d
		}
	}
	d := rt.baseDelay
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= rt.maxDelay {
			d = rt.maxDelay
			break
		}
	}
	return rt.jitter(d)
}

// parseRetryAfter parses a Retry-After header value: either delay-seconds (a
// non-negative integer) or an HTTP-date.
func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// fullJitter spreads a backoff delay over [d/2, d]: it keeps a growing floor so
// successive backoffs still increase, while randomizing the exact wait to avoid
// a thundering herd of synchronized retries.
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// ctxSleep waits d, returning false early if ctx is canceled first.
func ctxSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
