package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// step is one scripted outcome: status 0 means "return a transport error",
// otherwise an HTTP response with that status (and optional headers).
type step struct {
	status int
	header http.Header
}

// scriptRT serves a fixed sequence of outcomes and records the body bytes seen
// on each attempt (to prove a retried request re-sends its full payload).
type scriptRT struct {
	t      *testing.T
	steps  []step
	calls  int
	bodies []string
}

func (s *scriptRT) RoundTrip(req *http.Request) (*http.Response, error) {
	i := s.calls
	s.calls++
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.bodies = append(s.bodies, string(b))
		_ = req.Body.Close()
	} else {
		s.bodies = append(s.bodies, "")
	}
	if i >= len(s.steps) {
		s.t.Fatalf("unexpected attempt %d: only %d scripted", i+1, len(s.steps))
	}
	st := s.steps[i]
	if st.status == 0 {
		return nil, errors.New("transport boom")
	}
	h := st.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: st.status, Header: h, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

// newRetry builds a retryTransport over inner with deterministic, non-blocking
// seams: identity jitter and a sleep that records each delay instead of waiting.
func newRetry(t *testing.T, inner http.RoundTripper, maxRetries int) (*retryTransport, *[]time.Duration) {
	t.Helper()
	rt, ok := WithRetry(inner, RetryOptions{MaxRetries: maxRetries}).(*retryTransport)
	if !ok {
		t.Fatalf("WithRetry did not return a *retryTransport for MaxRetries=%d", maxRetries)
	}
	delays := &[]time.Duration{}
	rt.jitter = func(d time.Duration) time.Duration { return d } // deterministic
	rt.sleep = func(_ context.Context, d time.Duration) bool { *delays = append(*delays, d); return true }
	return rt, delays
}

func newReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var r *http.Request
	var err error
	if body == "" {
		r, err = http.NewRequest(method, url, nil)
	} else {
		r, err = http.NewRequest(method, url, strings.NewReader(body))
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return r
}

const apiURL = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/abc/tracks/internal"

func TestRetry_5xxThenSuccess(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 500}, {status: 200}}}
	rt, delays := newRetry(t, inner, 1)
	resp, err := rt.RoundTrip(newReq(t, http.MethodPut, apiURL, "payload"))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if inner.calls != 2 {
		t.Errorf("attempts = %d, want 2 (1 retry)", inner.calls)
	}
	if len(*delays) != 1 {
		t.Errorf("backoff sleeps = %d, want 1", len(*delays))
	}
}

func TestRetry_429HonorsRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "2")
	inner := &scriptRT{t: t, steps: []step{{status: 429, header: h}, {status: 200}}}
	rt, delays := newRetry(t, inner, 1)
	resp, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, ""))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("final status = %d, want 200", resp.StatusCode)
	}
	if len(*delays) != 1 || (*delays)[0] != 2*time.Second {
		t.Errorf("429 backoff = %v, want exactly [2s] (Retry-After honored)", *delays)
	}
}

func TestRetry_transportErrorThenSuccess(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 0}, {status: 200}}}
	rt, _ := newRetry(t, inner, 2)
	resp, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, ""))
	if err != nil {
		t.Fatalf("RoundTrip after transport-error recovery: %v", err)
	}
	if resp.StatusCode != 200 || inner.calls != 2 {
		t.Errorf("status=%d calls=%d, want 200 in 2 attempts", resp.StatusCode, inner.calls)
	}
}

func TestRetry_4xxNotRetried(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		inner := &scriptRT{t: t, steps: []step{{status: code}}}
		rt, delays := newRetry(t, inner, 3)
		resp, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, ""))
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if resp.StatusCode != code || inner.calls != 1 || len(*delays) != 0 {
			t.Errorf("status %d: calls=%d delays=%d, want a single attempt with no retry", code, inner.calls, len(*delays))
		}
	}
}

func TestRetry_editsCommitNeverRetried(t *testing.T) {
	const commitURL = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/edits/abc:commit"
	inner := &scriptRT{t: t, steps: []step{{status: 500}}}
	rt, _ := newRetry(t, inner, 3)
	resp, err := rt.RoundTrip(newReq(t, http.MethodPost, commitURL, ""))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("final status = %d, want 500 returned (no retry)", resp.StatusCode)
	}
	if inner.calls != 1 {
		t.Errorf("edits.commit attempts = %d, want 1 (never auto-retried — could double-publish)", inner.calls)
	}
}

func TestRetry_withoutRetryContextNeverRetried(t *testing.T) {
	// A request whose context carries WithoutRetry (the resumable-upload
	// chunk PUTs) must pass through once even on a 5xx — the caller owns its
	// own resume-from-offset recovery, so a blind transport retry would
	// double-send bytes.
	inner := &scriptRT{t: t, steps: []step{{status: 500}}}
	rt, _ := newRetry(t, inner, 3)
	req := newReq(t, http.MethodPut, apiURL, "chunk")
	req = req.WithContext(WithoutRetry(req.Context()))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("final status = %d, want 500 returned (no retry)", resp.StatusCode)
	}
	if inner.calls != 1 {
		t.Errorf("WithoutRetry attempts = %d, want 1 (caller owns recovery)", inner.calls)
	}
}

func TestRetry_exhaustedReturnsLastResponse(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 500}, {status: 500}, {status: 500}}}
	rt, delays := newRetry(t, inner, 2)
	resp, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, ""))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("final status = %d, want 500 (exhausted)", resp.StatusCode)
	}
	if inner.calls != 3 {
		t.Errorf("attempts = %d, want 3 (initial + 2 retries)", inner.calls)
	}
	if len(*delays) != 2 {
		t.Errorf("backoff sleeps = %d, want 2", len(*delays))
	}
}

func TestRetry_uploadBodyRecreatedPerAttempt(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 500}, {status: 200}}}
	rt, _ := newRetry(t, inner, 1)
	resp, err := rt.RoundTrip(newReq(t, http.MethodPost, apiURL, "payload-bytes"))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if len(inner.bodies) != 2 {
		t.Fatalf("recorded %d bodies, want 2", len(inner.bodies))
	}
	for i, b := range inner.bodies {
		if b != "payload-bytes" {
			t.Errorf("attempt %d body = %q, want the full payload re-sent", i+1, b)
		}
	}
}

func TestRetry_backoffGrowsExponentially(t *testing.T) {
	inner := &scriptRT{t: t, steps: []step{{status: 500}, {status: 500}, {status: 200}}}
	rt, delays := newRetry(t, inner, 2)
	if _, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, "")); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if len(*delays) != 2 {
		t.Fatalf("backoff sleeps = %d, want 2", len(*delays))
	}
	// Identity jitter → delays are base, base*2: strictly increasing.
	if (*delays)[1] <= (*delays)[0] {
		t.Errorf("backoff did not grow: %v", *delays)
	}
}

func TestWithRetry_zeroIsPassthrough(t *testing.T) {
	inner := &scriptRT{t: t}
	got := WithRetry(inner, RetryOptions{MaxRetries: 0})
	if got != http.RoundTripper(inner) {
		t.Errorf("WithRetry(MaxRetries=0) should return inner unchanged (no retry layer)")
	}
}
