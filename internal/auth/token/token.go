// Package token mints OAuth2 access tokens for the Google Play Developer
// API from a parsed service account. The HTTP client used for the JWT
// exchange is injectable via the oauth2.HTTPClient context key so tests
// (and any future custom transport) drop in a mock RoundTripper.
package token

import (
	"context"
	"encoding/json"
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
// Reporting API: the read-only post-launch quality service (crashes/ANR
// vitals, #49). It is a DISTINCT scope from AndroidPublisherScope: `gplay
// vitals` commands request only this one (least privilege), and the publishing
// surface never requests it.
const ReportingScope = "https://www.googleapis.com/auth/playdeveloperreporting"

// StorageReadOnlyScope is the OAuth2 scope required to read the developer's
// Google Cloud Storage reporting bucket: the monthly CSV exports (reviews
// history, #94). Like ReportingScope it is a DISTINCT, least-privilege scope:
// `gplay reviews history` requests only this one (read-only by construction),
// and no publishing or reporting command requests it. Documented at:
// https://cloud.google.com/storage/docs/authentication
const StorageReadOnlyScope = "https://www.googleapis.com/auth/devstorage.read_only"

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
// AndroidPublisherScope (the publishing surface's contract) so existing
// callers are unaffected; a `gplay vitals` command passes ReportingScope for
// least-privilege access to the read-only reporting service (#49). Errors from
// the exchange are wrapped in *AuthError when they are an auth refusal (see
// isAuthRefusal).
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
		if re.Response != nil && isAuthRefusal(re) {
			return nil, &AuthError{
				StatusCode: re.Response.StatusCode,
				Body:       string(re.Body),
				Cause:      err,
			}
		}
	}
	return nil, err
}

// credentialRefusals are the RFC 6749 §5.2 error codes that mean "this
// credential will never mint a token": Google answers a deleted or disabled
// key, a bad JWT signature or a skewed clock with HTTP 400 invalid_grant, not
// 401, so the status alone misses the most common refusal.
var credentialRefusals = map[string]bool{
	"invalid_grant":       true,
	"unauthorized_client": true,
	"invalid_client":      true,
}

// isAuthRefusal reports whether a token-endpoint failure is a permanent auth
// refusal (exit 10) rather than a transient upstream fault: any 401/403, or a
// 400 whose error code names the credential. Other 400s (invalid_scope, a
// malformed request) stay unwrapped, as do 5xx.
func isAuthRefusal(re *oauth2.RetrieveError) bool {
	switch re.Response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		return credentialRefusals[retrieveErrorCode(re)]
	}
	return false
}

// retrieveErrorCode returns the RFC 6749 `error` parameter of a token-endpoint
// failure. The jwt flow gplay uses builds its RetrieveError without parsing
// the body (ErrorCode stays empty), so the JSON body is the fallback source.
func retrieveErrorCode(re *oauth2.RetrieveError) string {
	if re.ErrorCode != "" {
		return re.ErrorCode
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(re.Body, &body) != nil {
		return ""
	}
	return body.Error
}
