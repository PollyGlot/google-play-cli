// Package token mints OAuth2 access tokens for the Google Play Developer
// API from a parsed service account. The HTTP client used for the JWT
// exchange is injectable via the oauth2.HTTPClient context key so tests
// (and any future custom transport) drop in a mock RoundTripper.
package token

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
)

// AndroidPublisherScope is the OAuth2 scope required to talk to the Google
// Play Developer API. Documented at:
// https://developers.google.com/android-publisher/authorization
const AndroidPublisherScope = "https://www.googleapis.com/auth/androidpublisher"

// ReportingScope is the OAuth2 scope required to talk to the Play Developer
// Reporting API — the read-only post-launch quality service (crashes/ANR
// vitals, #49). It is a DISTINCT scope from AndroidPublisherScope: `gplay
// vitals` commands request only this one (least privilege), and the publishing
// surface never requests it.
const ReportingScope = "https://www.googleapis.com/auth/playdeveloperreporting"

// AuthError wraps an HTTP error from the OAuth2 token endpoint so callers
// (and the command layer) can map it to exit code 10.
type AuthError struct {
	StatusCode int
	Body       string
	Cause      error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("token: oauth2 exchange failed (%d): %s", e.StatusCode, e.Body)
}

func (e *AuthError) Unwrap() error { return e.Cause }

// ExitCode satisfies exit.Coder: a refused JWT exchange is an auth failure.
func (*AuthError) ExitCode() int { return 10 }

// Source returns an oauth2.TokenSource that lazily mints access tokens for the
// requested scopes by signing a JWT with the service-account key and exchanging
// it at sa.TokenURI. When no scope is passed it defaults to
// AndroidPublisherScope — the publishing surface's contract — so existing
// callers are unaffected; a `gplay vitals` command passes ReportingScope for
// least-privilege access to the read-only reporting service (#49). Errors from
// the exchange are wrapped in *AuthError when the HTTP status indicates an auth
// failure.
func Source(ctx context.Context, sa *serviceaccount.ServiceAccount, scopes ...string) (oauth2.TokenSource, error) {
	if len(scopes) == 0 {
		scopes = []string{AndroidPublisherScope}
	}
	cfg, err := google.JWTConfigFromJSON(sa.Raw, scopes...)
	if err != nil {
		return nil, err
	}
	return &wrappedSource{inner: cfg.TokenSource(ctx)}, nil
}

type wrappedSource struct {
	inner oauth2.TokenSource
}

func (w *wrappedSource) Token() (*oauth2.Token, error) {
	tok, err := w.inner.Token()
	if err == nil {
		return tok, nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.Response != nil && isAuthStatus(re.Response.StatusCode) {
			return nil, &AuthError{
				StatusCode: re.Response.StatusCode,
				Body:       string(re.Body),
				Cause:      err,
			}
		}
	}
	return nil, err
}

func isAuthStatus(code int) bool {
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}
