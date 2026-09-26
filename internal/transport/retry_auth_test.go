package transport

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

// authRefusedRT stands in for oauth2.Transport when the /token exchange is
// refused: the request never leaves, RoundTrip fails with the wrapped
// *token.AuthError, and every call is counted.
type authRefusedRT struct{ calls int }

func (a *authRefusedRT) RoundTrip(*http.Request) (*http.Response, error) {
	a.calls++
	return nil, fmt.Errorf("oauth2 transport: %w", &token.AuthError{StatusCode: 400, Body: `{"error":"invalid_grant"}`})
}

// TestRetry_authRefusalNotRetried (#584): a refused token exchange is a dead
// credential, not a transient fault, so --retry must fail it on the first
// attempt instead of re-sending it N times.
func TestRetry_authRefusalNotRetried(t *testing.T) {
	inner := &authRefusedRT{}
	rt, delays := newRetry(t, inner, 3)
	_, err := rt.RoundTrip(newReq(t, http.MethodGet, apiURL, ""))
	var authErr *token.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("RoundTrip err = %v, want the *token.AuthError surfaced", err)
	}
	if inner.calls != 1 || len(*delays) != 0 {
		t.Errorf("calls=%d delays=%d, want a single attempt with no backoff", inner.calls, len(*delays))
	}
}
