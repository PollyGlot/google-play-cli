package transport

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

const (
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 30 * time.Second
)

// RetryOptions configures WithRetry.
type RetryOptions struct {
	// MaxRetries is the number of *additional* attempts after the first
	// (0 disables retry: WithRetry is then a no-op passthrough).
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
// (honoring Retry-After, capped at MaxDelay) are retried up to opts.MaxRetries
// times with exponential backoff plus jitter. Other 4xx (auth, validation) are
// never retried: retrying them just wastes time.
//
// Only an idempotent request is replayed after an attempt that may have
// reached the server (a 5xx, a reset, a per-attempt timeout). Idempotency is
// the method's declared bit in internal/apiregistry, so a create, an append or
// a money-moving action is retried only on an outcome that proves it was never
// applied: a dial or DNS failure, or a 429. `edits.commit` is never retried at
// all (its exit-60 conflict classification already guides the caller).
// Request bodies are recreated per attempt via Request.GetBody, so a retried
// upload re-sends from a fresh reader. MaxRetries == 0 returns inner unchanged.
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
		if !apiregistry.IdempotentRequest(req) && !neverApplied(resp, err) {
			return resp, err
		}
		// Drain + close the discarded response so the connection can be reused.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if !rt.sleep(req.Context(), rt.backoff(attempt, resp)) {
			// Context canceled during backoff: return the last result instead
			// of spinning through doomed attempts.
			return resp, err
		}
	}
}

// retryable reports whether a (resp, err) outcome warrants another attempt:
// any transport error other than a refused token exchange, an HTTP 429, or
// any 5xx. A 2xx or a non-429 4xx is terminal.
func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return !isAuthRefusal(err)
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

// neverApplied reports whether a failed attempt provably did not reach the
// server's write path, the only case where a non-idempotent request may be
// sent again: the connection was never opened (dial or DNS failure), or the
// server refused it up front with a 429. A 5xx, a reset or a timeout after the
// request left the machine proves nothing: the write may have landed.
func neverApplied(resp *http.Response, err error) bool {
	if err == nil {
		return resp != nil && resp.StatusCode == http.StatusTooManyRequests
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// isAuthRefusal reports whether a transport error is a refused OAuth2 token
// exchange. oauth2.Transport mints the token inside RoundTrip, so a revoked or
// invalid key surfaces here as a plain transport error; replaying it only
// re-sends a dead credential N times before failing the same way.
func isAuthRefusal(err error) bool {
	var authErr *token.AuthError
	return errors.As(err, &authErr)
}

// isNonRetryable excludes the operations that must never auto-retry:
//   - edits.commit (path suffix ":commit"): a duplicate commit could
//     double-publish.
//   - any request whose context carries WithoutRetry: the resumable-upload
//     helper drives its own resume-from-offset loop (query the committed
//     offset via a 308 probe, then continue), so a blind transport retry of
//     a chunk PUT would double-send bytes and race the helper's own recovery.
func isNonRetryable(req *http.Request) bool {
	if req.Context().Value(noRetryKey{}) != nil {
		return true
	}
	return strings.HasSuffix(req.URL.Path, ":commit")
}

// noRetryKey is the private context key set by WithoutRetry.
type noRetryKey struct{}

// WithoutRetry marks ctx so that any request issued with it is excluded from
// the WithRetry middleware's automatic retry loop (transport error / 5xx /
// 429). It is used by callers that implement their own recovery: notably the
// resumable-upload helper, whose chunk PUTs must not be blindly re-sent.
func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRetryKey{}, struct{}{})
}

// cloneWithFreshBody clones req with a body rebuilt from GetBody, so every
// attempt sends the full payload from the start. NewRequestWithContext sets
// GetBody automatically for the in-memory body types gplay uses (bytes/strings
// readers), and the upload paths set it explicitly, so a replayable body is
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
// header is honored up to MaxDelay: a server asking for an hour would otherwise
// park a CI job that long, and --timeout bounds each attempt, not the sleep in
// between. Otherwise the delay is exponential (base * 2^attempt, capped at
// MaxDelay) with jitter.
func (rt *retryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), rt.maxDelay); ok {
			return d
		}
	}
	return rt.jitter(exponential(attempt, rt.baseDelay, rt.maxDelay))
}

// exponential is base * 2^attempt, capped at maxDelay, before jitter.
func exponential(attempt int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= maxDelay {
			return maxDelay
		}
	}
	return d
}

// Backoff is the delay --retry waits before its attempt number attempt+1 (500ms
// doubling to a 30s cap, with jitter), exported so a caller that owns its own
// recovery loop (the resumable-upload helper, which --retry must not touch)
// paces itself on the same curve instead of a second, drifting one.
func Backoff(attempt int) time.Duration {
	return fullJitter(exponential(attempt, defaultRetryBaseDelay, defaultRetryMaxDelay))
}

// Sleep waits d and reports true, or returns false as soon as ctx is done: a
// Ctrl-C or a CI cancel must never sit out a 30s backoff.
func Sleep(ctx context.Context, d time.Duration) bool { return ctxSleep(ctx, d) }

// parseRetryAfter parses a Retry-After header value, either delay-seconds (a
// non-negative integer) or an HTTP-date, and clamps the result to [0, maxDelay].
// The seconds are compared to the cap BEFORE the multiplication: a huge value
// would overflow time.Duration into a negative delay and retry at once.
func parseRetryAfter(v string, maxDelay time.Duration) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		if secs > int64(maxDelay/time.Second) {
			return maxDelay, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return min(max(time.Until(t), 0), maxDelay), true
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
