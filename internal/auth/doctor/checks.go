package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

var (
	mEditsInsert = apiregistry.MustResolve("androidpublisher.edits.insert")
	mEditsDelete = apiregistry.MustResolve("androidpublisher.edits.delete")
)

const (
	exitAuth    = 10
	exitAuthz   = 11
	exitAPI4xx  = 30
	exitAPI5xx  = 40
	exitNetwork = 50
)

func DefaultChecks(obs *transport.ScopeObserver) []Check {
	return []Check{
		CheckSAJSONValid(),
		CheckOAuth2Mint(),
		CheckScope(obs),
		CheckReportingScope(obs),
	}
}

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

func missingFieldHint(field string) string {
	return "service account JSON is missing required field \"" + field +
		"\"; re-download the JSON from Google Cloud Console and run `gplay auth login`"
}

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

func CheckOAuth2Mint() Check {
	return Check{
		Name:     "OAuth2 access token can be minted",
		ExitCode: exitAuth,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			probe := &transportProbe{}
			_, ts, fail := tokenSource(ctx, sa, probe.wrap(ctx, hc))
			if fail != nil {
				return *fail
			}
			if _, err := ts.Token(); err != nil {
				if probe.err != nil {
					return CheckResult{
						Passed:   false,
						ExitCode: exitAuth,
						Hint:     "could not reach the token endpoint (" + err.Error() + "); this is a network failure, not a key problem: check the connection, DNS, proxy and TLS settings, then retry",
					}
				}
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

type transportProbe struct {
	inner http.RoundTripper
	err   error
}

func (p *transportProbe) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := p.inner.RoundTrip(req)
	if err != nil {
		p.err = err
		return resp, err
	}
	resp.Body = probedBody{ReadCloser: resp.Body, probe: p}
	return resp, nil
}

type probedBody struct {
	io.ReadCloser
	probe *transportProbe
}

func (b probedBody) Read(buf []byte) (int, error) {
	n, err := b.ReadCloser.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		b.probe.err = err
	}
	return n, err
}

func (p *transportProbe) wrap(ctx context.Context, hc *http.Client) *http.Client {
	if hc == nil {
		hc, _ = ctx.Value(oauth2.HTTPClient).(*http.Client)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	c := *hc
	p.inner = c.Transport
	if p.inner == nil {
		p.inner = http.DefaultTransport
	}
	c.Transport = p
	return &c
}

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

func CheckPackageAccess(packageName string) Check {
	return Check{
		Name:     "Service account can edit " + packageName,
		ExitCode: exitAuthz,
		Run: func(ctx context.Context, sa *serviceaccount.ServiceAccount, hc *http.Client) CheckResult {
			ctx, ts, fail := tokenSource(ctx, sa, hc)
			if fail != nil {
				return *fail
			}
			httpClient := oauth2.NewClient(ctx, ts)

			editID, insertResult, done := insertEdit(ctx, httpClient, sa, packageName)
			if done {
				return insertResult
			}

			if deleteResult, failed := deleteEdit(ctx, httpClient, packageName, editID); failed {
				return deleteResult
			}
			return CheckResult{Passed: true}
		},
	}
}

func insertEdit(ctx context.Context, httpClient *http.Client, sa *serviceaccount.ServiceAccount, packageName string) (string, CheckResult, bool) {
	raw, err := api.Do(ctx, httpClient, api.Call{
		Method: mEditsInsert, Target: packageName,
		Params: map[string]string{"packageName": packageName},
		Body:   []byte{},
	})
	if err != nil {
		status, msg := failure(err)
		if status == 0 {
			return "", CheckResult{
				Passed:   false,
				ExitCode: exitNetwork,
				Hint:     "network failure on edits.insert for " + packageName + ": safe to retry (" + msg + ")",
			}, true
		}
		return "", insertFailureResult(status, msg, sa, packageName), true
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.ID == "" {
		return "", CheckResult{
			Passed:   false,
			ExitCode: exitAPI5xx,
			Hint:     "edits.insert for " + packageName + " returned a malformed body: no Edit ID to discard",
		}, true
	}
	return parsed.ID, CheckResult{}, false
}

func failure(err error) (int, string) {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.StatusCode, ae.Message
	}
	return 0, err.Error()
}

func statusToExitCode(status int) int {
	switch {
	case status == http.StatusUnauthorized:
		return exitAuth
	case status == http.StatusForbidden:
		return exitAuthz
	case status >= 500:
		return exitAPI5xx
	default:
		return exitAPI4xx
	}
}

func insertFailureResult(status int, msg string, sa *serviceaccount.ServiceAccount, packageName string) CheckResult {
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
			Hint:     fmt.Sprintf("temporary upstream failure: safe to retry (HTTP %d from edits.insert on %s)", status, packageName),
		}
	}
	return CheckResult{
		Passed:   false,
		ExitCode: exit,
		Hint:     fmt.Sprintf("%s (HTTP %d from edits.insert on %s)", msg, status, packageName),
	}
}

func deleteEdit(ctx context.Context, httpClient *http.Client, packageName, editID string) (CheckResult, bool) {
	_, err := api.Do(ctx, httpClient, api.Call{
		Method: mEditsDelete, Target: packageName,
		Params: map[string]string{"packageName": packageName, "editId": editID},
	})
	if err == nil {
		return CheckResult{}, false
	}
	status, msg := failure(err)
	if status == 0 {
		return CheckResult{
			Passed:   false,
			ExitCode: exitNetwork,
			Hint:     "edits.insert on " + packageName + " succeeded but cleanup failed: safe to retry (" + msg + ")",
		}, true
	}
	exit := statusToExitCode(status)
	if status >= 500 {
		return CheckResult{
			Passed:   false,
			ExitCode: exit,
			Hint:     fmt.Sprintf("edits.insert on %s succeeded but cleanup failed: temporary upstream failure, safe to retry (HTTP %d)", packageName, status),
		}, true
	}
	return CheckResult{
		Passed:   false,
		ExitCode: exit,
		Hint:     fmt.Sprintf("edits.insert on %s succeeded but cleanup failed: %s (HTTP %d)", packageName, msg, status),
	}, true
}
