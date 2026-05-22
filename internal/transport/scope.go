// Package transport exposes the HTTP middleware gplay layers on top of
// the oauth2 client. Today the only middleware is ScopeObserver, which
// captures the OAuth2 JWT exchange request body so doctor's scope check
// can assert against the requested scopes without poking at ctx.Value.
package transport

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// maxObservedBody caps the in-memory copy ScopeObserver retains of the
// JWT exchange request body. Real payloads are a few KB; capping the
// observed slice at 64 KB stops a malformed or hostile server from
// blowing up RAM. The wire payload itself is passed through unchanged
// — only the observation buffer is bounded.
const maxObservedBody = 64 * 1024

// ScopeObserver wraps an http.RoundTripper and captures the OAuth2
// scope claim from the token exchange request body. The observer is
// safe to share across requests; a typical doctor invocation only does
// one token exchange so the captured value is single-use.
type ScopeObserver struct {
	mu       sync.Mutex
	captured string
}

// observerTransport is the wrapping RoundTripper installed by WithScopeObserver.
type observerTransport struct {
	inner http.RoundTripper
	obs   *ScopeObserver
}

// WithScopeObserver returns a RoundTripper that delegates to inner
// while recording the token-exchange request body, plus the observer
// the caller queries after the exchange runs. Pass the returned
// RoundTripper into an http.Client's Transport field; ScopeObserver is
// the read-side handle.
//
// inner may be nil; the wrapper falls back to http.DefaultTransport.
func WithScopeObserver(inner http.RoundTripper) (http.RoundTripper, *ScopeObserver) {
	if inner == nil {
		inner = http.DefaultTransport
	}
	obs := &ScopeObserver{}
	return &observerTransport{inner: inner, obs: obs}, obs
}

// RoundTrip implements http.RoundTripper. It reads the full request
// body so the inner transport receives an exact copy, while keeping
// only the first maxObservedBody bytes as the observation buffer.
// Truncating the wire payload would silently corrupt requests larger
// than the cap; bounding only the observation slice keeps memory safe
// without lying to the inner transport.
func (t *observerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, err := io.ReadAll(req.Body)
		if err == nil {
			observed := buf
			if len(observed) > maxObservedBody {
				observed = observed[:maxObservedBody]
			}
			t.obs.mu.Lock()
			t.obs.captured = string(observed)
			t.obs.mu.Unlock()
			req.Body = io.NopCloser(bytes.NewReader(buf))
			req.ContentLength = int64(len(buf))
		}
	}
	return t.inner.RoundTrip(req)
}

// Scopes returns the requested scopes from the most recently observed
// token-exchange. Empty if no exchange has happened yet or the body
// could not be parsed.
func (o *ScopeObserver) Scopes() []string {
	o.mu.Lock()
	captured := o.captured
	o.mu.Unlock()
	if captured == "" {
		return nil
	}
	values, err := url.ParseQuery(captured)
	if err != nil {
		return nil
	}
	raw := values.Get("scope")
	if raw == "" {
		// JWT-config flows put the scopes inside the assertion JWT's
		// payload rather than a separate form param, so peel them out
		// of there as a fallback.
		return scopesFromAssertion(values.Get("assertion"))
	}
	return strings.Fields(raw)
}

// scopesFromAssertion extracts the "scope" claim from a base64url
// JWT payload without verifying the signature. Returns nil if the JWT
// can't be parsed.
func scopesFromAssertion(jwt string) []string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil
	}
	const key = `"scope":"`
	idx := strings.Index(payload, key)
	if idx < 0 {
		return nil
	}
	rest := payload[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return nil
	}
	return strings.Fields(rest[:end])
}

func base64URLDecode(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
