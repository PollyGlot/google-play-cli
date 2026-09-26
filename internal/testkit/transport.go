package testkit

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// TokenURL is the OAuth2 token endpoint ServiceAccountJSON declares and the
// only URL the Fake answers with a bearer token.
const TokenURL = "https://oauth2.googleapis.com/token"

// tokenBody is the canned token-exchange response.
const tokenBody = `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`

// IsTokenRequest reports whether r targets TokenURL exactly. A suffix match
// (any path ending in /token) would hand a bearer token to a misdirected auth
// host or to an API path that happens to end in /token, and the test would
// pass for the wrong reason.
func IsTokenRequest(r *http.Request) bool {
	return r.URL.Scheme+"://"+r.URL.Host+r.URL.Path == TokenURL && r.URL.RawQuery == ""
}

// TokenResponse answers the token exchange when r is one (see IsTokenRequest),
// so a hand-rolled RoundTripper can delegate the auth hop. It returns
// (nil, false) for any other request.
func TokenResponse(r *http.Request) (*http.Response, bool) {
	if !IsTokenRequest(r) {
		return nil, false
	}
	return Response(http.StatusOK, tokenBody), true
}

// Response builds an application/json response with status and body.
func Response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// Call records one API request the Fake served (token exchanges excluded).
type Call struct {
	Method string
	Host   string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
	// URL is the request URL as sent, escaping included (Path is decoded).
	URL string
	// ContentLength is the length the request declared (-1 unknown).
	ContentLength int64
	// Replayable reports whether the --retry transport could re-send the
	// request: it has no body, or a GetBody to re-open it.
	Replayable bool
}

// Responder decides the response for a Call. ok=false falls through to the
// next responder; a status of 0 means 200.
type Responder func(c Call) (status int, body string, ok bool)

// Fake is an http.RoundTripper standing in for the Play APIs: it terminates
// the token exchange at TokenURL, records every other request, and serves it
// from the first matching responder. A request no responder claims fails the
// round trip, so an unexpected call surfaces as an error instead of a silent
// empty 200.
type Fake struct {
	responders []Responder

	mu     sync.Mutex
	calls  []Call
	tokens int
}

// NewFake builds a Fake whose responders are tried in order per request.
func NewFake(responders ...Responder) *Fake { return &Fake{responders: responders} }

// RoundTrip implements http.RoundTripper.
func (f *Fake) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if resp, ok := TokenResponse(req); ok {
		f.mu.Lock()
		f.tokens++
		f.mu.Unlock()
		return resp, nil
	}
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("testkit: read request body: %w", err)
		}
		body = b
	}
	c := Call{
		Method: req.Method,
		Host:   req.URL.Host,
		Path:   req.URL.Path,
		Query:  req.URL.RawQuery,
		Header: req.Header.Clone(),
		Body:   body,

		URL:           req.URL.String(),
		ContentLength: req.ContentLength,
		Replayable:    req.Body == nil || req.Body == http.NoBody || req.GetBody != nil,
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	for _, r := range f.responders {
		if status, b, ok := r(c); ok {
			if status == 0 {
				status = http.StatusOK
			}
			return Response(status, b), nil
		}
	}
	return nil, fmt.Errorf("testkit: no responder for %s %s", req.Method, req.URL.Redacted())
}

// Calls returns the recorded API calls in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// TokenExchanges returns how many token requests the Fake answered.
func (f *Fake) TokenExchanges() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}

// Wrote reports whether any recorded call used a mutating method (POST, PUT,
// PATCH, DELETE): the assertion that a refused write reached no API.
func (f *Fake) Wrote() bool {
	for _, c := range f.Calls() {
		switch c.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			return true
		}
	}
	return false
}

// Any answers every call with status and body: the catch-all responder to
// put last when unclaimed calls should succeed rather than fail.
func Any(status int, body string) Responder {
	return func(Call) (int, string, bool) { return status, body, true }
}
