package kernel_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// tokenScriptRT is the single injected transport for both the /token exchange
// and the API calls. The token endpoint answers with the scripted outcome
// (tokenErr as a transport failure, else tokenStatus + tokenBody); any request
// that gets past it to the API is a test failure, since a refused credential
// must never reach androidpublisher.
type tokenScriptRT struct {
	t           *testing.T
	tokenStatus int
	tokenBody   string
	tokenErr    error
	tokenCalls  int
}

func (s *tokenScriptRT) serve(req *http.Request) (*http.Response, error) {
	if !testkit.IsTokenRequest(req) {
		s.t.Errorf("request reached %s past a failed token exchange", req.URL)
		return nil, errors.New("unexpected API request")
	}
	s.tokenCalls++
	if s.tokenErr != nil {
		return nil, s.tokenErr
	}
	return testkit.Response(s.tokenStatus, s.tokenBody), nil
}

// runReadOnlyEdit drives the `tracks list` shape end to end through the
// kernel under --output json: resolve the Account, build the authed client,
// open a read-only Edit. It returns the surfaced error and the envelope.
func runReadOnlyEdit(t *testing.T, rt http.RoundTripper, retry int) (envelopeShape, error) {
	t.Helper()
	boot := newBoot(t)
	var stdout bytes.Buffer
	boot.Stdout = &stdout
	in := kernel.Inputs{
		Ctx:      context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt}),
		Format:   output.FormatJSON,
		Retry:    retry,
		Resolver: resolver.Inputs{ServiceAccountFlag: string(signedAccount(t).Raw)},
	}
	err := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
		hc, err := rc.AuthedClient()
		if err != nil {
			return nil, err
		}
		return nil, edits.WithReadOnlyEdit(rc.Ctx, hc, "com.example.app", func(string) error { return nil })
	})
	if err == nil {
		t.Fatal("expected the run to fail")
	}
	return decodeOneEnvelope(t, stdout.String()), err
}

// TestRun_tokenRefused_exit10NotRetried (#584): a /token exchange refused
// with 400 invalid_grant (Google's answer for a deleted key, a bad signature
// or clock skew) is an auth failure, exit 10 AUTH_FAILED and not retryable
// (DESIGN §9), and --retry 3 sends exactly one token request instead of
// replaying the dead credential.
func TestRun_tokenRefused_exit10NotRetried(t *testing.T) {
	for _, status := range []int{400, 401} {
		rt := &tokenScriptRT{t: t, tokenStatus: status, tokenBody: `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`}
		env, err := runReadOnlyEdit(t, testkit.RoundTripFunc(rt.serve), 3)
		if got := exit.For(err); got != 10 {
			t.Errorf("token %d: exit = %d, want 10; err=%v", status, got, err)
		}
		if env.Error.Code != string(exit.CodeAuthFailed) || env.Error.ExitCode != 10 || env.Error.Retryable {
			t.Errorf("token %d: envelope = %+v, want AUTH_FAILED, exitCode 10, retryable false", status, env.Error)
		}
		if rt.tokenCalls != 1 {
			t.Errorf("token %d: %d token requests under --retry 3, want 1", status, rt.tokenCalls)
		}
	}
}

// TestRun_tokenNetworkFailure_staysExit50 is the counterpart: a genuine
// transport failure reaching the token endpoint (no HTTP response) is still a
// retry-safe network error.
func TestRun_tokenNetworkFailure_staysExit50(t *testing.T) {
	rt := &tokenScriptRT{t: t, tokenErr: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}
	env, err := runReadOnlyEdit(t, testkit.RoundTripFunc(rt.serve), 0)
	if got := exit.For(err); got != 50 {
		t.Errorf("exit = %d, want 50; err=%v", got, err)
	}
	if env.Error.Code != string(exit.CodeNetworkError) || !env.Error.Retryable {
		t.Errorf("envelope = %+v, want NETWORK_ERROR and retryable", env.Error)
	}
}
