package testkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// RoundTripFunc adapts a function to an http.RoundTripper: the one adapter
// type a test needs when the Fake's status-and-body responders cannot express
// its case (a transport error, a body that fails mid-read, a response header,
// a request that must block). Answer the token hop with TokenResponse and
// build responses with Response rather than re-declaring either.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ReadBody returns the request body, or nil when the request has none: a
// bodiless POST reaches a transport with a nil Body, which io.ReadAll cannot
// read.
func ReadBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(r.Body)
	return b
}

// Wire renders calls in a stable text form for a golden file: the method, the
// full URL as sent (escaping included), every header in sorted order, the
// declared ContentLength, whether the retry transport could re-send the body,
// and the body bytes. Two request builders that render the same Wire put the
// same bytes on the network, which is the proof a migration onto the executor
// owes (the golden is recorded before the migration and must not move).
func Wire(calls []Call) string {
	var b strings.Builder
	for _, c := range calls {
		fmt.Fprintf(&b, "%s %s\n", c.Method, c.URL)
		keys := make([]string, 0, len(c.Header))
		for k := range c.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(c.Header[k], ", "))
		}
		fmt.Fprintf(&b, "content-length=%d replayable=%t\n", c.ContentLength, c.Replayable)
		if len(c.Body) > 0 {
			fmt.Fprintf(&b, "%s\n", c.Body)
		}
	}
	return b.String()
}

// Exchange runs send against a fresh Fake serving responders and returns the
// Wire of what it sent followed by its outcome: the value send returned, or
// the error text with its exit code (the Coder contract exit.For reads). A
// table of Exchanges, one per module function and answer, recorded in a
// golden is what pins a module's requests, results and error mapping byte for
// byte.
func Exchange(name string, send func(hc *http.Client) (any, error), responders ...Responder) string {
	fake := NewFake(responders...)
	got, err := send(&http.Client{Transport: fake})
	out := "## " + name + "\n" + Wire(fake.Calls())
	if err == nil {
		return out + "=> ok " + render(got) + "\n\n"
	}
	code := -1
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		code = coder.ExitCode()
	}
	return out + fmt.Sprintf("=> error (exit %d): %v\n\n", code, err)
}

// render prints a returned value: bytes verbatim (a pass-through body need not
// be JSON), anything else as JSON, falling back to %+v.
func render(v any) string {
	switch b := v.(type) {
	case []byte:
		return string(b)
	case json.RawMessage:
		return string(b)
	}
	if j, err := json.Marshal(v); err == nil {
		return string(j)
	}
	return fmt.Sprintf("%+v", v)
}

// Send is one named request-sending function of a module, for Exchanges.
type Send struct {
	Name string
	Fn   func(hc *http.Client) (any, error)
}

// Answer is one canned way for the fake API to reply, for Exchanges.
type Answer struct {
	Name       string
	Responders []Responder
}

// The answers every module table starts from: a 2xx that decodes, a refusal
// carrying Google's error envelope, and a 2xx whose body is not JSON.
var (
	AnswerOK        = Answer{Name: "ok", Responders: []Responder{Any(http.StatusOK, `{}`)}}
	AnswerDenied    = Answer{Name: "denied", Responders: []Responder{Any(http.StatusForbidden, `{"error":{"code":403,"message":"The caller does not have permission","errors":[{"reason":"forbidden"}]}}`)}}
	AnswerMalformed = Answer{Name: "malformed", Responders: []Responder{Any(http.StatusOK, `not json`)}}
)

// Exchanges runs every Send against every Answer and concatenates the
// Exchange records in order: the body of a module's wire golden.
func Exchanges(sends []Send, answers ...Answer) string {
	var b strings.Builder
	for _, s := range sends {
		for _, a := range answers {
			b.WriteString(Exchange(s.Name+" / "+a.Name, s.Fn, a.Responders...))
		}
	}
	return b.String()
}

// Sequence answers successive calls with bodies in order (status 200), then
// repeats the last one: the pages of a paginated listing. A fresh Sequence is
// needed per Exchange, since it counts the calls it served.
func Sequence(bodies ...string) Responder {
	var (
		mu sync.Mutex
		i  int
	)
	return func(Call) (int, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		b := bodies[len(bodies)-1]
		if i < len(bodies) {
			b = bodies[i]
		}
		i++
		return http.StatusOK, b, true
	}
}
