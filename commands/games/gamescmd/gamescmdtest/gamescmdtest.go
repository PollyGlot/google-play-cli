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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// RTFunc adapts a function to an http.RoundTripper.
type RTFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f RTFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Token answers the OAuth2 /token exchange with a canned bearer token, so a
// leaf test's RoundTripper can delegate the auth hop and focus on the API call.
// Returns (resp, true) when r is the token request, (nil, false) otherwise.
func Token(r *http.Request) (*http.Response, bool) {
	// Match only the exact token endpoint from saJSON()'s token_uri, not any
	// path ending in /token, so a misdirected auth host or an API path that
	// happens to end in /token can't get a fake bearer token and pass for the
	// wrong reason.
	if r.URL.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`))}, true
	}
	return nil, false
}

// JSONResp builds a 200 application/json response carrying body.
func JSONResp(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// StatusResp builds a response with an arbitrary status and JSON body.
func StatusResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func saJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

// NewRC builds a RunContext wired with a throwaway service account and rt as
// the transport for both the token exchange and the API calls. Format is JSON
// so tests can assert the verbatim pass-through.
func NewRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}
