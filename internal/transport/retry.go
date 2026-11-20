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

type RetryOptions struct {
	MaxRetries int
	Timeout    time.Duration
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

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
	jitter     func(time.Duration) time.Duration
	sleep      func(ctx context.Context, d time.Duration) bool
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
		attemptClient := &http.Client{Transport: rt.inner, Timeout: rt.timeout}
		resp, err = attemptClient.Do(attemptReq)

		if excluded || attempt >= rt.maxRetries || !retryable(resp, err) {
			return resp, err
		}
		if !apiregistry.IdempotentRequest(req) && !neverApplied(resp, err) {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if !rt.sleep(req.Context(), rt.backoff(attempt, resp)) {
			return resp, err
		}
	}
}

func retryable(resp *http.Response, err error) bool {
	if err != nil {
		var insecure *InsecureURLError
		return !isAuthRefusal(err) && !errors.As(err, &insecure)
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

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

func isAuthRefusal(err error) bool {
	var authErr *token.AuthError
	return errors.As(err, &authErr)
}

func isNonRetryable(req *http.Request) bool {
	if req.Context().Value(noRetryKey{}) != nil {
		return true
	}
	return strings.HasSuffix(req.URL.Path, ":commit")
}

type noRetryKey struct{}

func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRetryKey{}, struct{}{})
}

func cloneWithFreshBody(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}
	if req.GetBody == nil {
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

func (rt *retryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), rt.maxDelay); ok {
			return d
		}
	}
	return rt.jitter(exponential(attempt, rt.baseDelay, rt.maxDelay))
}

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

func Backoff(attempt int) time.Duration {
	return fullJitter(exponential(attempt, defaultRetryBaseDelay, defaultRetryMaxDelay))
}

func Sleep(ctx context.Context, d time.Duration) bool { return ctxSleep(ctx, d) }

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

func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

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
