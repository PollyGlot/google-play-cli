package token_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// exchange is what one token exchange put on the wire: the endpoint, the
// grant type, and the decoded JWT assertion minus its per-run times and
// signature.
type exchange struct {
	URL       string
	GrantType string
	Header    map[string]any
	Claims    map[string]any
	Lifetime  float64 // exp - iat, in seconds
}

// captureExchange mints one token through Source on a fake token endpoint and
// returns the exchange it sent.
func captureExchange(t *testing.T, sa *serviceaccount.ServiceAccount, scopes ...string) exchange {
	t.Helper()
	var got exchange
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		form, err := url.ParseQuery(string(testkit.ReadBody(req)))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		got.URL = req.URL.String()
		got.GrantType = form.Get("grant_type")
		parts := strings.Split(form.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatalf("assertion has %d parts, want 3", len(parts))
		}
		got.Header = decodeSegment(t, parts[0])
		got.Claims = decodeSegment(t, parts[1])
		exp, _ := got.Claims["exp"].(float64)
		iat, _ := got.Claims["iat"].(float64)
		got.Lifetime = exp - iat
		delete(got.Claims, "exp")
		delete(got.Claims, "iat")
		resp, _ := testkit.TokenResponse(req)
		return resp, nil
	})})
	ts, err := token.Source(ctx, sa, scopes...)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	return got
}

func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
	return m
}

// testkitSA parses a key file on the testkit key, with a private_key_id so
// the JWT header's kid is exercised.
func testkitSA(t *testing.T, keyType string) *serviceaccount.ServiceAccount {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":           keyType,
		"project_id":     testkit.ProjectID,
		"private_key_id": "0123456789abcdef",
		"private_key":    string(testkit.PrivateKeyPEM(t)),
		"client_email":   testkit.ClientEmail,
		"token_uri":      testkit.TokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	sa, err := serviceaccount.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sa
}

// TestSource_pinsTheAssertion (#646) pins the JWT the token exchange sends.
// The expected values were recorded by signing the testkit key through both
// google.JWTConfigFromJSON and Source and comparing the two exchanges
// (TestSource_matchesJWTConfigFromJSON, commit 8e31f34 of PR #646); with the
// oauth2/google import gone, this pin is what holds them equal.
func TestSource_pinsTheAssertion(t *testing.T) {
	sa := testkitSA(t, "service_account")
	for _, tc := range []struct {
		scopes    []string
		wantScope string
	}{
		{nil, token.AndroidPublisherScope},
		{[]string{token.ReportingScope}, token.ReportingScope},
		{[]string{token.AndroidPublisherScope, token.StorageReadOnlyScope}, token.AndroidPublisherScope + " " + token.StorageReadOnlyScope},
	} {
		got := captureExchange(t, sa, tc.scopes...)
		want := exchange{
			URL:       testkit.TokenURL,
			GrantType: "urn:ietf:params:oauth:grant-type:jwt-bearer",
			Header:    map[string]any{"alg": "RS256", "typ": "JWT", "kid": "0123456789abcdef"},
			Claims: map[string]any{
				"iss":   testkit.ClientEmail,
				"scope": tc.wantScope,
				"aud":   testkit.TokenURL,
			},
			Lifetime: 3600,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scopes %v:\n got  %+v\n want %+v", tc.scopes, got, want)
		}
	}
}

// TestSource_refusesANonServiceAccountKey: JWTConfigFromJSON refused any key
// type but service_account before any network call; Source keeps that.
func TestSource_refusesANonServiceAccountKey(t *testing.T) {
	for _, keyType := range []string{"authorized_user", "external_account", ""} {
		_, err := token.Source(context.Background(), testkitSA(t, keyType))
		if err == nil || !strings.Contains(err.Error(), `expected "service_account"`) {
			t.Errorf("type %q: err = %v, want a refusal naming service_account", keyType, err)
		}
	}
}
