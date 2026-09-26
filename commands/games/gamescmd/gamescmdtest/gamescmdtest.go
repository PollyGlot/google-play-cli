// Package gamescmdtest is the test-only seam shared by the `gplay games` leaf
// tests: a RoundTripper that fakes the OAuth2 /token exchange plus a RunContext
// wired with a throwaway service account, so each leaf test asserts addressing
// and pass-through without re-copying the auth dance (mirrors the harness the
// device-tiers leaf tests inline).
//
// Production code must not import this package.
package gamescmdtest

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// RTFunc adapts a function to an http.RoundTripper.
type RTFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RTFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Token answers the OAuth2 /token exchange with a canned bearer token, so a
// leaf test's RoundTripper can delegate the auth hop and focus on the API call.
// Returns (resp, true) when r is the token request, (nil, false) otherwise.
// The match is testkit's exact token URL, never a path suffix.
func Token(r *http.Request) (*http.Response, bool) { return testkit.TokenResponse(r) }

// JSONResp builds a 200 application/json response carrying body.
func JSONResp(body string) *http.Response { return testkit.Response(http.StatusOK, body) }

// StatusResp builds a response with an arbitrary status and JSON body.
func StatusResp(status int, body string) *http.Response { return testkit.Response(status, body) }

// NewRC builds a RunContext wired with a throwaway service account and rt as
// the transport for both the token exchange and the API calls. Format is JSON
// so tests can assert the verbatim pass-through.
func NewRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}
