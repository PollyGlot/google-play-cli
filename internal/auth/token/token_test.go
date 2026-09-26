package token_test

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// roundTripperFunc is the canonical pattern documented in CLAUDE.md: a
// function type that implements http.RoundTripper, so each test wires up
// the response shape it needs without a wrapper interface.
type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// makeTestSA builds a valid *ServiceAccount with a real RSA
// private key so JWTConfigFromJSON can actually sign the token-exchange JWT.
func makeTestSA(t *testing.T) *serviceaccount.ServiceAccount {
	t.Helper()
	key := testkit.RSAKey(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509Marshal(t, key),
	})

	body := map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "ci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sa, err := serviceaccount.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sa
}

func x509Marshal(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return b
}

func ctxWithRT(t *testing.T, fn roundTripperFunc) context.Context {
	t.Helper()
	httpClient := &http.Client{Transport: fn}
	return context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
}

func TestSource_mintsToken_on200(t *testing.T) {
	sa := makeTestSA(t)
	called := false
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		called = true
		if req.URL.String() != "https://oauth2.googleapis.com/token" {
			t.Errorf("RoundTrip URL = %q", req.URL)
		}
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})

	ts, err := token.Source(ctx, sa)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if !called {
		t.Error("token endpoint was not called")
	}
	if tok.AccessToken != "abc.def.ghi" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "abc.def.ghi")
	}
}

func TestSource_returnsAuthError_on401(t *testing.T) {
	sa := makeTestSA(t)
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		body := `{"error":"invalid_grant","error_description":"signature mismatch"}`
		return &http.Response{
			StatusCode: 401,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	ts, err := token.Source(ctx, sa)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	_, err = ts.Token()
	if err == nil {
		t.Fatal("Token: expected error on 401, got nil")
	}
	var ae *token.AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("Token: got %T (%v), want *token.AuthError", err, err)
	}
	if ae.StatusCode != 401 {
		t.Errorf("AuthError.StatusCode = %d, want 401", ae.StatusCode)
	}
}

// TestSource_classifiesTokenRefusals (#584): Google refuses a deleted key, a
// bad signature or a skewed clock with 400 invalid_grant, so a 400 naming the
// credential is an *AuthError (exit 10) like a 401/403. A 400 about the
// request itself and a 5xx stay plain errors (not a credential verdict).
func TestSource_classifiesTokenRefusals(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantAuth bool
	}{
		{"400 invalid_grant", 400, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`, true},
		{"400 unauthorized_client", 400, `{"error":"unauthorized_client"}`, true},
		{"400 invalid_client", 400, `{"error":"invalid_client"}`, true},
		{"401 invalid_grant", 401, `{"error":"invalid_grant"}`, true},
		{"403 no body", 403, ``, true},
		{"400 invalid_scope", 400, `{"error":"invalid_scope"}`, false},
		{"400 unparseable body", 400, `<html>bad request</html>`, false},
		{"503", 503, `{"error":"backend_error"}`, false},
	}
	sa := makeTestSA(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				}, nil
			})
			ts, err := token.Source(ctx, sa)
			if err != nil {
				t.Fatalf("Source: %v", err)
			}
			_, err = ts.Token()
			if err == nil {
				t.Fatalf("Token: expected an error on HTTP %d", tc.status)
			}
			var ae *token.AuthError
			if got := errors.As(err, &ae); got != tc.wantAuth {
				t.Errorf("errors.As(*AuthError) = %v, want %v; err=%v", got, tc.wantAuth, err)
			}
		})
	}
}
