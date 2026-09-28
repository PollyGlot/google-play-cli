package testkit

import "net/http"

// RoundTripFunc adapts a function to an http.RoundTripper, for the cases a
// Fake responder cannot express: a transport error, a response header (a
// resumable session's Location), a body that fails mid-read, a request that
// blocks until its context ends. Delegate the auth hop to TokenResponse and
// build answers with Response, so the closure keeps only what the test is
// about.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
