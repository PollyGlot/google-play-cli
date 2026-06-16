package doctor

// This file holds the individual Check constructors. Keeping the
// definitions here (rather than as package-level functions hard-baked
// into the runner in doctor.go) lets the command layer compose its own
// ordered chain and lets the per-package round-trip check (issue #12)
// drop in alongside these without editing the runner.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// maxAPIBodyRead caps how many bytes of an androidpublisher API
// response body we hold in memory. Error envelopes are small; the cap
// stops a malformed or hostile server from blowing up RAM.
const maxAPIBodyRead = 64 * 1024

// Exit codes per docs/DESIGN.md §9.
const (
	exitAuth        = 10 // 10 — auth (credential invalid, token mint failed, scope missing)
	exitAuthz       = 11 // 11 — authorization (403 — SA not invited on the app, etc.)
	exitAPI4xx      = 30 // 30 — API 4xx other than auth/perms (not found, conflict, gone)
	exitAPI5xx      = 40 // 40 — API 5xx (upstream temporarily unhealthy)
	exitNetwork     = 50 // 50 — transport-level failure (timeout, DNS, refused)
	androidPubHost  = "https://androidpublisher.googleapis.com"
	androidPubBase  = androidPubHost + "/androidpublisher/v3"
	editsPathFmt    = "/applications/%s/edits"
	editsItemPathFm = "/applications/%s/edits/%s"
)

// DefaultChecks returns the ordered chain used by `gplay auth doctor`
// in its non-API form (issue #11). The per-package edits.insert/delete
// round-trip (issue #12) appends one more Check to this slice.
//
// obs is the ScopeObserver wired around the doctor's HTTP client at the
// command layer; CheckScope reads from it to assert the requested
// scopes. Pass nil only in tests that don't exercise CheckScope.
func DefaultChecks(obs *transport.ScopeObserver) []Check {
	return []Check{
		CheckSAJSONValid(),
		CheckOAuth2Mint(),
		CheckScope(obs),
		CheckReportingScope(obs),
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

// tokenSource threads the runner's HTTP client into ctx (so a test-injected
// RoundTripper covers the /token exchange) and mints an OAuth2 token source
// from sa. On failure it returns a ready-to-surface auth CheckResult. The
// returned context carries the base client, so a caller that builds an
// oauth2.NewClient on top sees the same seam for the androidpublisher calls.
//
// This is the shared opening of every API-touching check; each check adds
// its own ts.Token() / round-trip logic on top.
func tokenSource(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) (context.Context, oauth2.TokenSource, *CheckResult) {
	if hc != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	}
	ts, err := token.Source(ctx, sa)
	if err != nil {
		return ctx, nil, &CheckResult{
			Passed:   false,
			ExitCode: exitAuth,
			Hint:     "could not build token source: " + err.Error(),
		}
	}
	return ctx, ts, nil
}

// CheckOAuth2Mint asserts that an OAuth2 access token can be minted by
// signing a JWT with the SA's private key and exchanging it at the
// configured token_uri. Failure (non-2xx response, signature problem,
// any transport error) is mapped to exit code 10. hc is the http.Client
// the runner threads down — the doctor command wraps it with the scope
// observer, but for this check that wrapping is invisible.
func CheckOAuth2Mint() Check {
	return Check{
		Name:     "OAuth2 access token can be minted",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			_, ts, fail := tokenSource(ctx, sa, hc)
			if fail != nil {
				return *fail
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
// package carries the AndroidPublisher scope. It reads observed scopes
// from obs (which the caller wires via transport.WithScopeObserver) and
// fails when the required scope is absent.
//
// obs may be nil — the check then reports a wiring bug instead of a
// credential problem.
//
// The optional requiredScope variadic exists for the scope-drift
// failure test; production callers pass only obs and the check pins
// to token.AndroidPublisherScope.
func CheckScope(obs *transport.ScopeObserver, requiredScope ...string) Check {
	required := token.AndroidPublisherScope
	if len(requiredScope) > 0 {
		required = requiredScope[0]
	}
	return Check{
		Name:     "Token carries the androidpublisher scope",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			if obs == nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "scope check requires a ScopeObserver; this indicates a bug in gplay's auth wiring",
				}
			}
			_, ts, fail := tokenSource(ctx, sa, hc)
			if fail != nil {
				return *fail
			}
			if _, err := ts.Token(); err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "token exchange failed before scope could be verified: " + err.Error(),
				}
			}

			scopes := obs.Scopes()
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

// CheckReportingScope asserts that a token can be minted for the
// playdeveloperreporting scope — the DISTINCT, read-only scope the `gplay
// vitals` commands request (#49). It mints a reporting-scoped token source and
// confirms the JWT exchange both succeeds AND requested that scope (read back
// from obs). Like CheckScope it is a wiring + credential check: a valid
// service-account key can always mint a token for the requested scope, so a
// failure here means either the credential cannot sign the exchange or gplay
// failed to request the scope. (API-level access to the reporting service, if
// the SA is not granted it, still surfaces as a 403 at call time.)
//
// obs may be nil — the check then reports a wiring bug. The optional
// requiredScope variadic exists for the scope-drift test; production callers
// pass only obs and the check pins to token.ReportingScope.
func CheckReportingScope(obs *transport.ScopeObserver, requiredScope ...string) Check {
	required := token.ReportingScope
	if len(requiredScope) > 0 {
		required = requiredScope[0]
	}
	return Check{
		Name:     "Token can be minted for the playdeveloperreporting scope",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			if obs == nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "scope check requires a ScopeObserver; this indicates a bug in gplay's auth wiring",
				}
			}
			if hc != nil {
				ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
			}
			ts, err := token.Source(ctx, sa, token.ReportingScope)
			if err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "could not build reporting token source: " + err.Error(),
				}
			}
			if _, err := ts.Token(); err != nil {
				return CheckResult{
					Passed:   false,
					ExitCode: exitAuth,
					Hint:     "token endpoint refused the reporting-scope JWT exchange (" + err.Error() + "); verify the service account key is valid",
				}
			}
			scopes := obs.Scopes()
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

// CheckPackageAccess opens then immediately discards an Edit on
// packageName via the Google Play Developer API, surfacing the
// "SA not invited on the app" 403 case with an actionable hint
// pointing at Play Console → Setup → API access.
//
// The check ALWAYS attempts edits.delete after a successful
// edits.insert — even on cleanup failure — so the Edit is never left
// dangling on the user's package. A cleanup failure is reported (so
// the user knows the round-trip didn't complete cleanly), but the
// rest of the doctor chain proceeds normally per the runner contract.
//
// HTTP status → exit-code mapping (per docs/DESIGN.md §9):
//
//	200/201        → Passed,  exit 0
//	403            → Failed,  exit 11 (authorization — SA not invited)
//	404            → Failed,  exit 30 (package not visible to SA)
//	other 4xx      → Failed,  exit 30 (API misuse)
//	5xx            → Failed,  exit 40 (transient upstream)
//	transport err  → Failed,  exit 50 (network)
func CheckPackageAccess(packageName string) Check {
	return Check{
		Name:     "Service account can edit " + packageName,
		ExitCode: exitAuthz,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			ctx, ts, fail := tokenSource(ctx, sa, hc)
			if fail != nil {
				return *fail
			}
			// oauth2.NewClient inherits ctx's oauth2.HTTPClient for the
			// underlying transport, so a test-injected RoundTripper sees
			// BOTH the /token exchange and the androidpublisher calls.
			httpClient := oauth2.NewClient(ctx, ts)

			editID, insertResult, done := insertEdit(ctx, httpClient, sa, packageName)
			if done {
				return insertResult
			}

			// Always attempt cleanup, even if we already have a healthy
			// insert. A leaked Edit blocks the user's next publish for
			// up to 24h, so cleanup failures are surfaced as the
			// check's outcome.
			if deleteResult, failed := deleteEdit(ctx, httpClient, packageName, editID); failed {
				return deleteResult
			}
			return CheckResult{Passed: true}
		},
	}
}

// insertEdit POSTs edits.insert and returns the new Edit ID on success.
// The third return value is true when the caller should return the
// CheckResult directly (i.e. on any non-2xx insert). When true, no
// delete attempt is made — there is no Edit ID to clean up.
func insertEdit(ctx context.Context, httpClient *http.Client, sa *serviceaccount.ServiceAccount, packageName string) (string, CheckResult, bool) {
	u := androidPubBase + fmt.Sprintf(editsPathFmt, url.PathEscape(packageName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
	if err != nil {
		return "", CheckResult{
			Passed:   false,
			ExitCode: exitNetwork,
			Hint:     "could not build edits.insert request: " + err.Error(),
		}, true
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", CheckResult{
			Passed:   false,
			ExitCode: exitNetwork,
			Hint:     "network failure on edits.insert for " + packageName + " — safe to retry (" + err.Error() + ")",
		}, true
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyRead))

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var parsed struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" {
			// Edit succeeded but we can't extract the ID — there is
			// nothing to clean up, so report a 5xx-shaped failure: the
			// upstream gave us a malformed body.
			return "", CheckResult{
				Passed:   false,
				ExitCode: exitAPI5xx,
				Hint:     "edits.insert for " + packageName + " returned a malformed body — no Edit ID to discard",
			}, true
		}
		return parsed.ID, CheckResult{}, false
	}

	return "", insertFailureResult(resp.StatusCode, body, sa, packageName), true
}

// statusToExitCode maps a Google Play API HTTP status to the gplay
// exit-code taxonomy (docs/DESIGN.md §9). Shared by both `edits.insert`
// and `edits.delete` failure paths.
func statusToExitCode(status int) int {
	switch {
	case status == http.StatusForbidden:
		return exitAuthz
	case status >= 500:
		return exitAPI5xx
	default:
		return exitAPI4xx
	}
}

// insertFailureResult maps an edits.insert non-2xx status to the
// CheckResult shape required by the issue.
func insertFailureResult(status int, body []byte, sa *serviceaccount.ServiceAccount, packageName string) CheckResult {
	exit := statusToExitCode(status)
	switch {
	case status == http.StatusForbidden:
		return CheckResult{
			Passed:   false,
			ExitCode: exit,
			Hint: "Service account is not granted access to " + packageName +
				". In Play Console → Setup → API access, grant the appropriate permission to " +
				sa.ClientEmail + ".",
		}
	case status == http.StatusNotFound:
		return CheckResult{
			Passed:   false,
			ExitCode: exit,
			Hint:     "Package " + packageName + " not found in Play Console under this service account's organization.",
		}
	case status >= 500:
		return CheckResult{
			Passed:   false,
			ExitCode: exit,
			Hint:     fmt.Sprintf("temporary upstream failure — safe to retry (HTTP %d from edits.insert on %s)", status, packageName),
		}
	}
	return CheckResult{
		Passed:   false,
		ExitCode: exit,
		Hint:     fmt.Sprintf("%s (HTTP %d from edits.insert on %s)", apiErrorMessage(body, status), status, packageName),
	}
}

// deleteEdit issues edits.delete to clean up an Edit opened by the
// happy-path insert. Returns (result, true) when the cleanup itself
// failed and the caller should surface that as the check outcome.
func deleteEdit(ctx context.Context, httpClient *http.Client, packageName, editID string) (CheckResult, bool) {
	u := androidPubBase + fmt.Sprintf(editsItemPathFm, url.PathEscape(packageName), url.PathEscape(editID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, http.NoBody)
	if err != nil {
		return CheckResult{
			Passed:   false,
			ExitCode: exitNetwork,
			Hint:     "could not build edits.delete request for " + packageName + ": " + err.Error(),
		}, true
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return CheckResult{
			Passed:   false,
			ExitCode: exitNetwork,
			Hint:     "edits.insert on " + packageName + " succeeded but cleanup failed — safe to retry (" + err.Error() + ")",
		}, true
	}
	defer func() { _ = resp.Body.Close() }()
	// 200, 204, and any other 2xx are fine.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return CheckResult{}, false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyRead))
	exit := statusToExitCode(resp.StatusCode)
	if resp.StatusCode >= 500 {
		return CheckResult{
			Passed:   false,
			ExitCode: exit,
			Hint:     fmt.Sprintf("edits.insert on %s succeeded but cleanup failed — temporary upstream failure, safe to retry (HTTP %d)", packageName, resp.StatusCode),
		}, true
	}
	return CheckResult{
		Passed:   false,
		ExitCode: exit,
		Hint:     fmt.Sprintf("edits.insert on %s succeeded but cleanup failed: %s (HTTP %d)", packageName, apiErrorMessage(body, resp.StatusCode), resp.StatusCode),
	}, true
}

// apiErrorMessage extracts the most useful human-readable message from
// a Google API error envelope, falling back to a generic placeholder
// when the body is empty or unparseable.
func apiErrorMessage(body []byte, status int) string {
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return strings.TrimSpace(string(body))
}
