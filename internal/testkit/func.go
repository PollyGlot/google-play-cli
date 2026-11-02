package testkit

import (
	"bytes"
	"io"
	"mime"
	"net/http"
)

// RoundTripFunc adapts a function to an http.RoundTripper, for the cases a
// Fake responder cannot express: a transport error, a response header (a
// resumable session's Location), a body that fails mid-read, a request that
// blocks until its context ends. Delegate the auth hop to TokenResponse and
// build answers with Response, so the closure keeps only what the test is
// about.
//
// A JSON body that breaks a constraint Play documents panics, as on the Fake.
// Other bodies (media, streams a test reads at its own pace) reach the
// function untouched.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/json" && r.Body != nil && r.Body != http.NoBody {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		c := Call{Method: r.Method, Host: r.URL.Host, Path: r.URL.Path, URL: r.URL.String(), Body: body}
		if why := refusal(c, nil); why != "" {
			panic(why)
		}
	}
	return f(r)
}
