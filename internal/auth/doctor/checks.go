package doctor

// This file holds the individual Check constructors. Keeping the
// definitions here (rather than as package-level functions hard-baked
// into the runner in doctor.go) lets the command layer compose its own
// ordered chain and lets the per-package round-trip check (issue #12)
// drop in alongside these without editing the runner.

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

// Exit code for any auth-layer failure (per docs/DESIGN.md §9).
const exitAuth = 10

// DefaultChecks returns the ordered chain used by `gplay auth doctor`
// in its non-API form (issue #11). The per-package edits.insert/delete
// round-trip (issue #12) appends one more Check to this slice.
func DefaultChecks() []Check {
	return []Check{
		CheckSAJSONValid(),
		CheckOAuth2Mint(),
		CheckScope(),
	}
}

// CheckSAJSONValid asserts that the resolved service account is non-nil
// and carries the required Google SA JSON fields (client_email,
// private_key, token_uri). Resolution upstream of this check is expected
// to call serviceaccount.Parse, so by the time sa lands here we are
// effectively re-asserting the parse contract; this gives the doctor a
// concrete pass line to render even on a happy run.
//
// The command layer is responsible for translating a *resolution*
// failure (file missing, JSON malformed) into a synthetic failing
// CheckResult under this same Name, so the user sees check 1 fail in
// the ordered checklist rather than an opaque pre-check error.
func CheckSAJSONValid() Check {
	return Check{
		Name:     "Service account JSON is valid",
		ExitCode: exitAuth,
		Run: func(_ context.Context, sa *serviceaccount.ServiceAccount, _ *http.Client) CheckResult {
			if sa == nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "no service account resolved; run `gplay auth login`",
				}
			}
			for _, f := range [...]struct {
				name  string
				value string
			}{
				{"client_email", sa.ClientEmail},
				{"private_key", sa.PrivateKey},
				{"token_uri", sa.TokenURI},
			} {
				if f.value == "" {
					return CheckResult{
						Passed:   false,
						ExitCode: exitAuth,
						Hint:     missingFieldHint(f.name),
					}
				}
			}
			return CheckResult{Passed: true}
		},
	}
}

// missingFieldHint phrases the required-field failure for end users.
func missingFieldHint(field string) string {
	return "service account JSON is missing required field \"" + field +
		"\"; re-download the JSON from Google Cloud Console and run `gplay auth login`"
}

// CheckOAuth2Mint asserts that an OAuth2 access token can be minted by
// signing a JWT with the SA's private key and exchanging it at the
// configured token_uri. Failure (non-2xx response, signature problem,
// any transport error) is mapped to exit code 10.
//
// The HTTP transport used for the exchange is taken from
// ctx via oauth2.HTTPClient (set by the test harness) — see the
// token package for the underlying wiring.
func CheckOAuth2Mint() Check {
	return Check{
		Name:     "OAuth2 access token can be minted",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, _ *http.Client) CheckResult {
			ts, err := token.Source(ctx, sa)
			if err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "could not build token source: " + err.Error(),
				}
			}
			if _, err := ts.Token(); err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "token endpoint refused the JWT exchange (" + err.Error() + "); verify the service account is enabled in Google Cloud Console and the private key has not been revoked",
				}
			}
			return CheckResult{Passed: true}
		},
	}
}

// CheckScope asserts that the OAuth2 JWT exchange driven by the token
// package carries the AndroidPublisher scope. The check intercepts the
// outgoing HTTP request to the token endpoint, parses the `scope` field
// out of the form-encoded body, and confirms the AndroidPublisher scope
// is listed. This catches the case where the scope constant in the
// token package has drifted from the one Google requires for the
// androidpublisher API surface.
//
// The check requires that ctx already carries an oauth2.HTTPClient — in
// production it does not (the default transport is used); in tests, the
// fixtures inject one. The check wraps that client's transport so it
// can observe and forward the JWT exchange request.
//
// The optional requiredScope variadic exists for the "scope drift"
// failure test: end users always invoke CheckScope() and the check
// pins to token.AndroidPublisherScope.
func CheckScope(requiredScope ...string) Check {
	required := token.AndroidPublisherScope
	if len(requiredScope) > 0 {
		required = requiredScope[0]
	}
	return Check{
		Name:     "Token carries the androidpublisher scope",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, _ *http.Client) CheckResult {
			// Wrap whatever HTTP client ctx carries (the test injects
			// one; in prod it is the default transport) with a small
			// observer that records the request body of the token
			// exchange. The token exchange POSTs form data including
			// the scope field.
			base, _ := ctx.Value(oauth2.HTTPClient).(*http.Client)
			if base == nil {
				base = &http.Client{}
			}
			obs := &scopeObserver{inner: base.Transport}
			if obs.inner == nil {
				obs.inner = http.DefaultTransport
			}
			wrapped := *base
			wrapped.Transport = obs
			ctx = context.WithValue(ctx, oauth2.HTTPClient, &wrapped)

			ts, err := token.Source(ctx, sa)
			if err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "could not build token source: " + err.Error(),
				}
			}
			if _, err := ts.Token(); err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "token exchange failed before scope could be verified: " + err.Error(),
				}
			}

			scopes := obs.observedScopes()
			for _, s := range scopes {
				if s == required {
					return CheckResult{Passed: true}
				}
			}
			return CheckResult{
				Passed:   false,
				ExitCode: exitAuth,
				Hint:     "token exchange did not request scope " + required + " (observed: " + strings.Join(scopes, ", ") + "); this indicates a bug in gplay's auth wiring",
			}
		},
	}
}

// scopeObserver is an http.RoundTripper that records the scope claim
// sent in the JWT exchange request body, then delegates to its inner
// transport so the response still flows through unchanged.
type scopeObserver struct {
	inner    http.RoundTripper
	captured string
}

// maxObservedBody caps the in-memory copy made of an outgoing token
// exchange body. Real JWT-exchange payloads are a few KB; capping at
// 64 KB keeps a malformed or hostile server from blowing up memory.
const maxObservedBody = 64 * 1024

func (o *scopeObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, err := io.ReadAll(io.LimitReader(req.Body, maxObservedBody))
		if err == nil {
			o.captured = string(buf)
			// Restore the body so the inner transport can consume it.
			req.Body = io.NopCloser(strings.NewReader(o.captured))
			req.ContentLength = int64(len(buf))
		}
	}
	return o.inner.RoundTrip(req)
}

// observedScopes parses the captured form-encoded JWT-exchange body and
// returns the requested scopes (the JWT's "scope" claim is mirrored
// into the form's "scope" parameter by golang.org/x/oauth2).
func (o *scopeObserver) observedScopes() []string {
	values, err := url.ParseQuery(o.captured)
	if err != nil {
		return nil
	}
	raw := values.Get("scope")
	if raw == "" {
		// As a fallback, decode the assertion JWT and read its scope
		// claim. The JWT exchange flow used by JWTConfig embeds the
		// requested scopes inside the assertion's payload (not as a
		// separate form param), so this is the realistic path.
		return scopesFromAssertion(values.Get("assertion"))
	}
	return strings.Fields(raw)
}

// scopesFromAssertion extracts the "scope" claim from a base64url
// JWT payload without verifying the signature (we only inspect, we
// do not consume the token). Returns nil if the JWT can't be parsed.
func scopesFromAssertion(jwt string) []string {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil
	}
	// We avoid encoding/json here to keep the helper dependency-free;
	// the scope claim is a quoted string value in the JSON payload.
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

// base64URLDecode is a base64url decoder that tolerates the missing
// padding the JWT spec deliberately omits.
func base64URLDecode(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ResolutionFailure builds a synthetic check-1 failure for the case
// where the command layer could not even resolve a credential (no
// active account, file missing, JSON malformed). It is used by the
// `gplay auth doctor` command glue so the user always sees an ordered
// checklist instead of an opaque error before any check has run.
func ResolutionFailure(err error) []CheckResult {
	hint := err.Error()
	var mfe *serviceaccount.MissingFieldError
	if errors.As(err, &mfe) {
		hint = missingFieldHint(mfe.Field)
	}
	c := CheckSAJSONValid()
	return []CheckResult{{
		Name:     c.Name,
		Passed:   false,
		ExitCode: c.ExitCode,
		Hint:     hint,
	}}
}
