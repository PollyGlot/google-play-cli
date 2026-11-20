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

type RTFunc = testkit.RoundTripFunc

func Token(r *http.Request) (*http.Response, bool) { return testkit.TokenResponse(r) }

func JSONResp(body string) *http.Response { return testkit.Response(http.StatusOK, body) }

func StatusResp(status int, body string) *http.Response { return testkit.Response(status, body) }

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
