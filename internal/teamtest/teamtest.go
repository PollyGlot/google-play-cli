// Package teamtest is the shared test scaffolding for the `gplay team` command
// e2e suites: the team-specific responders and RunContext wiring on top of
// internal/testkit, which owns the transport fake and the service-account
// fixture.
//
// Production code must not import this package.
package teamtest

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
	"golang.org/x/oauth2"
)

// DeveloperID is the canonical test developer-account id the suites resolve to.
const DeveloperID = "4900000000000000000"

// Call records one captured API request (after the /token exchange).
type Call = testkit.Call

// Responder decides the response for a captured API call. ok=false falls
// through to the next responder; if none matches the transport serves 200 with
// an empty body.
type Responder = testkit.Responder

// RT is the mock transport: a testkit.Fake terminating the OAuth2 token
// exchange at its exact URL, recording every API call, and serving responses
// from its responders in order. Calls and Wrote come from the Fake.
type RT = testkit.Fake

// New builds a mock transport from responders, tried in order per request. An
// unclaimed call gets 200 with an empty body, which is what the team suites
// were written against (a bare Fake would fail the round trip instead).
func New(responders ...Responder) *RT {
	return testkit.NewFake(append(responders, testkit.Any(200, ""))...)
}

// UsersList responds to GET .../users with body. Use Pages for multi-page
// pagination.
func UsersList(body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/users") {
			return 200, body, true
		}
		return 0, "", false
	}
}

// Pages responds to successive GET .../users calls with the bodies in order
// (for paginated users.list); the final body should carry no nextPageToken.
func Pages(bodies ...string) Responder {
	var i int
	var mu sync.Mutex
	return func(c Call) (int, string, bool) {
		if c.Method != http.MethodGet || !strings.HasSuffix(c.Path, "/users") {
			return 0, "", false
		}
		mu.Lock()
		defer mu.Unlock()
		b := bodies[len(bodies)-1]
		if i < len(bodies) {
			b = bodies[i]
		}
		i++
		return 200, b, true
	}
}

// Fail forces status+body for any call whose method matches and whose path ends
// with pathSuffix: the error-mapping and write-path responders.
func Fail(method, pathSuffix string, status int, body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == method && strings.HasSuffix(c.Path, pathSuffix) {
			return status, body, true
		}
		return 0, "", false
	}
}

// NewRC builds a RunContext with a resolved Account, the mock transport
// threaded through ctx (covering both the /token exchange and the API calls),
// and Resolved.DeveloperID preset to DeveloperID so addressing resolves with no
// flag. Format is JSON.
func NewRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.AccountName = "ci-bot"
	rc.Resolved = &config.Resolved{DeveloperID: DeveloperID}
	return rc
}
