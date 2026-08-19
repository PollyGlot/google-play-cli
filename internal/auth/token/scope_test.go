package token_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
)

// scopeFromAssertion decodes the `scope` claim of the signed JWT bearer
// assertion the token exchange POSTs. The JWT bearer grant carries the
// requested scope inside the signed assertion (not as a top-level form field),
// so this is how a test observes which scope gplay actually requested.
func scopeFromAssertion(t *testing.T, req *http.Request) string {
	t.Helper()
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	assertion := req.PostFormValue("assertion")
	if assertion == "" {
		t.Fatal("token exchange carried no JWT assertion")
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion is not a JWT (%d parts)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims.Scope
}

func token200(_ *http.Request) (*http.Response, error) {
	body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}, nil
}

// TestSource_defaultsToAndroidPublisherScope asserts that with no explicit
// scope, Source keeps requesting the androidpublisher scope: the contract
// every existing publishing command relies on (least surprise on the default
// path).
func TestSource_defaultsToAndroidPublisherScope(t *testing.T) {
	sa := makeTestSA(t)
	var got string
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		got = scopeFromAssertion(t, req)
		return token200(req)
	})
	ts, err := token.Source(ctx, sa)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != token.AndroidPublisherScope {
		t.Errorf("default scope = %q, want %q", got, token.AndroidPublisherScope)
	}
}

// TestSource_honorsExplicitReportingScope asserts the per-command scope path:
// a vitals command requests ONLY the read-only playdeveloperreporting scope
// (least privilege, #49), never androidpublisher.
func TestSource_honorsExplicitReportingScope(t *testing.T) {
	sa := makeTestSA(t)
	var got string
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		got = scopeFromAssertion(t, req)
		return token200(req)
	})
	ts, err := token.Source(ctx, sa, token.ReportingScope)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != token.ReportingScope {
		t.Errorf("scope = %q, want %q", got, token.ReportingScope)
	}
	if strings.Contains(got, token.AndroidPublisherScope) {
		t.Errorf("vitals scope leaked androidpublisher: %q", got)
	}
}
