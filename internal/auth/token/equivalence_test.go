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
	"golang.org/x/oauth2/google"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// exchange is what one token source put on the wire: the endpoint, the grant
// type, and the decoded JWT assertion minus the per-run times and signature.
type exchange struct {
	URL       string
	GrantType string
	Header    map[string]any
	Claims    map[string]any
	Lifetime  float64 // exp - iat
}

func captureExchange(t *testing.T, ts func(ctx context.Context) oauth2.TokenSource) exchange {
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
		got.Lifetime = got.Claims["exp"].(float64) - got.Claims["iat"].(float64)
		delete(got.Claims, "exp")
		delete(got.Claims, "iat")
		resp, _ := testkit.TokenResponse(req)
		return resp, nil
	})})
	if _, err := ts(ctx).Token(); err != nil {
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

// TestSource_matchesJWTConfigFromJSON (#646): the jwt.Config built from the
// parsed key signs the same assertion google.JWTConfigFromJSON did, on the
// testkit key: same header (alg, typ, kid), claims (iss, scope, aud), lifetime
// and token endpoint.
func TestSource_matchesJWTConfigFromJSON(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type":           "service_account",
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
	for _, scopes := range [][]string{nil, {token.ReportingScope}, {token.AndroidPublisherScope, token.StorageReadOnlyScope}} {
		want := captureExchange(t, func(ctx context.Context) oauth2.TokenSource {
			s := scopes
			if len(s) == 0 {
				s = []string{token.AndroidPublisherScope}
			}
			cfg, err := google.JWTConfigFromJSON(raw, s...)
			if err != nil {
				t.Fatalf("JWTConfigFromJSON: %v", err)
			}
			return cfg.TokenSource(ctx)
		})
		got := captureExchange(t, func(ctx context.Context) oauth2.TokenSource {
			ts, err := token.Source(ctx, sa, scopes...)
			if err != nil {
				t.Fatalf("Source: %v", err)
			}
			return ts
		})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scopes %v:\n got  %+v\n want %+v", scopes, got, want)
		}
		t.Logf("scopes %v: %+v", scopes, got)
	}
}
