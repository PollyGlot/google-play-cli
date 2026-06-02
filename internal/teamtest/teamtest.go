// Package teamtest is the shared test scaffolding for the `gplay team` command
// e2e suites. It hides the OAuth2 /token + androidpublisher transport mock and
// the RunContext wiring behind a small responder model, so each command suite
// asserts external behaviour without re-deriving the seam.
//
// Production code must not import this package.
package teamtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"golang.org/x/oauth2"
)

// DeveloperID is the canonical test developer-account id the suites resolve to.
const DeveloperID = "4900000000000000000"

// Call records one captured API request (after the /token exchange).
type Call struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

// Responder decides the response for a captured API call. ok=false falls
// through to the next responder; if none matches the transport serves 200 with
// an empty body.
type Responder func(c Call) (status int, body string, ok bool)

// RT is a mock RoundTripper terminating the OAuth2 /token exchange and the
// androidpublisher developers/* calls. It records every API call and serves
// responses from its responders in order.
type RT struct {
	responders []Responder

	mu    sync.Mutex
	calls []Call
}

// New builds a mock transport from responders, tried in order per request.
func New(responders ...Responder) *RT { return &RT{responders: responders} }

// RoundTrip implements http.RoundTripper.
func (r *RT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return resp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	c := Call{Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	for _, rsp := range r.responders {
		if status, b, ok := rsp(c); ok {
			if status == 0 {
				status = 200
			}
			return resp(status, b), nil
		}
	}
	return resp(200, ""), nil
}

// Calls returns the captured non-token API calls in order.
func (r *RT) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call{}, r.calls...)
}

// Wrote reports whether any captured call used a mutating method (POST / PATCH
// / DELETE) — the assertion a refused write makes no network mutation.
func (r *RT) Wrote() bool {
	for _, c := range r.Calls() {
		switch c.Method {
		case http.MethodPost, http.MethodPatch, http.MethodDelete:
			return true
		}
	}
	return false
}

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// UsersList responds to GET .../users with body. Use Pages for multi-page
// pagination.
func UsersList(body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/users") {
			return 200, body, true
		}
		return 0, "", false
	}
}

// Pages responds to successive GET .../users calls with the bodies in order
// (for paginated users.list); the final body should carry no nextPageToken.
func Pages(bodies ...string) Responder {
	var i int
	var mu sync.Mutex
	return func(c Call) (int, string, bool) {
		if c.Method != http.MethodGet || !strings.HasSuffix(c.Path, "/users") {
			return 0, "", false
		}
		mu.Lock()
		defer mu.Unlock()
		b := bodies[len(bodies)-1]
		if i < len(bodies) {
			b = bodies[i]
		}
		i++
		return 200, b, true
	}
}

// Fail forces status+body for any call whose method matches and whose path ends
// with pathSuffix — the error-mapping and write-path responders.
func Fail(method, pathSuffix string, status int, body string) Responder {
	return func(c Call) (int, string, bool) {
		if c.Method == method && strings.HasSuffix(c.Path, pathSuffix) {
			return status, body, true
		}
		return 0, "", false
	}
}

// NewRC builds a RunContext with a resolved Account, the mock transport
// threaded through ctx (covering both the /token exchange and the API calls),
// and Resolved.DeveloperID preset to DeveloperID so addressing resolves with no
// flag. Format is JSON.
func NewRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.AccountName = "ci-bot"
	rc.Resolved = &config.Resolved{DeveloperID: DeveloperID}
	return rc
}

// saJSON mints a throwaway service-account JSON (real RSA key so the /token
// JWT signs), matching the other live command suites.
func saJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
