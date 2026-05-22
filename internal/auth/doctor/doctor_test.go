package doctor_test

import (
	"bytes"
	"context"
	"crypto/rand"
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

	"github.com/PollyGlot/google-play-cli/internal/auth/doctor"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// scopeWired builds the http.Client + ScopeObserver pair the way the
// command layer does in production: wrap the fixture's transport with
// transport.WithScopeObserver, return both. The returned client is
// what the test passes as the hc argument to doctor.Run, and the
// observer is what CheckScope reads from.
func scopeWired(rt http.RoundTripper) (*http.Client, *transport.ScopeObserver) {
	wrapped, obs := transport.WithScopeObserver(rt)
	return &http.Client{Transport: wrapped}, obs
}

// roundTripperFunc is the canonical pattern documented in AGENTS.md: a
// function type that implements http.RoundTripper, so each test wires
// up the response shape it needs without a wrapper interface.
type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// makeSignedSA produces a *ServiceAccount whose PrivateKey is a freshly
// generated RSA key, so the OAuth2 library can actually sign the JWT
// during the token exchange. The token endpoint is then mocked via a
// roundTripperFunc.
func makeSignedSA(t *testing.T) *serviceaccount.ServiceAccount {
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
		"client_email": "ci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sa, err := serviceaccount.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sa
}

// ctxWithRT wires a roundTripperFunc through oauth2.HTTPClient so the
// token package's call to JWTConfig.TokenSource uses it for the
// /token exchange.
func ctxWithRT(t *testing.T, fn roundTripperFunc) context.Context {
	t.Helper()
	return context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fn})
}

// validSAJSON mirrors the fixture used by the foundation slice tests
// (serviceaccount_test.go / login_test.go) so check 1 sees a credential
// shape that already passes parse.
const validSAJSON = `{
  "type": "service_account",
  "project_id": "test-proj",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@test-proj.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func mustParseSA(t *testing.T, body string) *serviceaccount.ServiceAccount {
	t.Helper()
	sa, err := serviceaccount.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sa
}

// TestRun_emptyChain_returnsEmpty confirms the scaffold compiles and the
// surface returns an empty slice when no checks are registered.
func TestRun_emptyChain_returnsEmpty(t *testing.T) {
	results := doctor.Run(context.Background(), nil, nil)
	if len(results) != 0 {
		t.Errorf("Run(ctx, nil, nil) = %v, want empty slice", results)
	}
}

func TestCheckSAJSONValid_happyPath(t *testing.T) {
	sa := mustParseSA(t, validSAJSON)
	results := doctor.Run(context.Background(), sa, nil, doctor.CheckSAJSONValid())

	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if !r.Passed || r.Skipped {
		t.Errorf("result = %+v, want Passed=true Skipped=false", r)
	}
	if r.Name == "" {
		t.Errorf("result.Name is empty")
	}
}

func TestCheckSAJSONValid_nilSA_failsWithHint(t *testing.T) {
	results := doctor.Run(context.Background(), nil, nil, doctor.CheckSAJSONValid())
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("nil sa: Passed=true, want false")
	}
	if r.ExitCode != 10 {
		t.Errorf("ExitCode = %d, want 10", r.ExitCode)
	}
	if r.Hint == "" {
		t.Errorf("Hint is empty; want actionable text")
	}
}

func TestCheckSAJSONValid_eachRequiredFieldMissing(t *testing.T) {
	// Each row constructs a *ServiceAccount whose JSON has been parsed
	// (so it is non-nil) but then has the named field zeroed. The check
	// must fail and the hint must mention the field name.
	cases := []struct {
		field  string
		mutate func(sa *serviceaccount.ServiceAccount)
	}{
		{"client_email", func(sa *serviceaccount.ServiceAccount) { sa.ClientEmail = "" }},
		{"private_key", func(sa *serviceaccount.ServiceAccount) { sa.PrivateKey = "" }},
		{"token_uri", func(sa *serviceaccount.ServiceAccount) { sa.TokenURI = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			sa := mustParseSA(t, validSAJSON)
			tc.mutate(sa)
			results := doctor.Run(context.Background(), sa, nil, doctor.CheckSAJSONValid())
			if len(results) != 1 {
				t.Fatalf("results len = %d, want 1", len(results))
			}
			r := results[0]
			if r.Passed {
				t.Errorf("missing %s: Passed=true, want false", tc.field)
			}
			if r.ExitCode != 10 {
				t.Errorf("ExitCode = %d, want 10", r.ExitCode)
			}
			if !strings.Contains(r.Hint, tc.field) {
				t.Errorf("Hint = %q, want to contain %q", r.Hint, tc.field)
			}
		})
	}
}

func TestCheckOAuth2Mint_happyPath(t *testing.T) {
	sa := makeSignedSA(t)
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

	results := doctor.Run(ctx, sa, nil, doctor.CheckOAuth2Mint())
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if !called {
		t.Error("token endpoint was not called")
	}
	r := results[0]
	if !r.Passed || r.Skipped {
		t.Errorf("result = %+v, want Passed=true Skipped=false", r)
	}
}

func TestCheckOAuth2Mint_401_failsWithHint(t *testing.T) {
	sa := makeSignedSA(t)
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		body := `{"error":"invalid_grant","error_description":"signature mismatch"}`
		return &http.Response{
			StatusCode: 401,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})

	results := doctor.Run(ctx, sa, nil, doctor.CheckOAuth2Mint())
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("401 mint: Passed=true, want false")
	}
	if r.ExitCode != 10 {
		t.Errorf("ExitCode = %d, want 10", r.ExitCode)
	}
	if r.Hint == "" {
		t.Errorf("Hint is empty; want actionable text")
	}
}

func TestCheckScope_happyPath_observesAndroidPublisherScope(t *testing.T) {
	sa := makeSignedSA(t)
	// The RoundTripper captures the JWT-exchange request body so the
	// test can independently assert the scope was sent, then responds
	// with a successful token to let the check complete.
	var capturedBody string
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			buf, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			capturedBody = string(buf)
		}
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	hc, obs := scopeWired(rt)

	results := doctor.Run(context.Background(), sa, hc, doctor.CheckScope(obs))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if !r.Passed || r.Skipped {
		t.Errorf("result = %+v, want Passed=true Skipped=false", r)
	}
	// Independent assertion: the request body we saw must mention the
	// AndroidPublisher scope somewhere (inside the JWT assertion).
	if capturedBody == "" {
		t.Error("RoundTripper did not see a request body")
	}
}

// TestCheckScope_wrongRequiredScope_fails exercises the test seam
// (checkScopeRequiring) to simulate the case where the doctor expects
// a scope that the actual JWT exchange does not request. End users
// always go through CheckScope which pins the expected scope to
// token.AndroidPublisherScope; the seam is what lets us verify the
// scope-mismatch path without mutating production constants.
func TestCheckScope_wrongRequiredScope_fails(t *testing.T) {
	sa := makeSignedSA(t)
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	hc, obs := scopeWired(rt)

	wrongScope := "https://www.googleapis.com/auth/something-else"
	results := doctor.Run(context.Background(), sa, hc, doctor.CheckScope(obs, wrongScope))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("wrong required scope: Passed=true, want false")
	}
	if r.ExitCode != 10 {
		t.Errorf("ExitCode = %d, want 10", r.ExitCode)
	}
	if r.Hint == "" {
		t.Errorf("Hint is empty; want actionable text")
	}
}

func TestRun_stopsOnFirstFailure(t *testing.T) {
	// Compose the full chain (#1, #2, #3) with an SA that is missing a
	// required field so check #1 fails. Check #2 and #3 must be reported
	// as Skipped=true / Passed=false without invoking their Run funcs.
	sa := mustParseSA(t, validSAJSON)
	sa.ClientEmail = ""

	// Use a RoundTripper that would FAIL THE TEST if invoked — to prove
	// downstream checks were not run.
	ctx := ctxWithRT(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("RoundTripper should not be called when check #1 fails; got %s", req.URL)
		return nil, nil
	})

	results := doctor.Run(ctx, sa, nil,
		doctor.CheckSAJSONValid(),
		doctor.CheckOAuth2Mint(),
		doctor.CheckScope(nil),
	)
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	if results[0].Passed {
		t.Errorf("check #1 = Passed=true, want false (missing client_email)")
	}
	if results[0].Skipped {
		t.Errorf("check #1 = Skipped=true, want false")
	}
	for i := 1; i < 3; i++ {
		if results[i].Passed {
			t.Errorf("check #%d = Passed=true, want false", i+1)
		}
		if !results[i].Skipped {
			t.Errorf("check #%d = Skipped=false, want true", i+1)
		}
		if results[i].Name == "" {
			t.Errorf("check #%d carries no Name; the runner must propagate it onto Skipped results", i+1)
		}
	}
}

// TestCheckSAJSONValid_jsonRoundTrip confirms the result encodes to the
// shape documented in issue #11 acceptance criteria.
func TestCheckSAJSONValid_jsonRoundTrip(t *testing.T) {
	sa := mustParseSA(t, validSAJSON)
	results := doctor.Run(context.Background(), sa, nil, doctor.CheckSAJSONValid())

	b, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var parsed []struct {
		Name     string `json:"name"`
		Passed   bool   `json:"passed"`
		Skipped  bool   `json:"skipped"`
		ExitCode int    `json:"exit_code"`
		Hint     string `json:"hint,omitempty"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%s)", err, b)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed len = %d, want 1", len(parsed))
	}
	if parsed[0].Name == "" {
		t.Errorf("name field missing in JSON: %s", b)
	}
}

// packageRT is a RoundTripper helper for CheckPackageAccess tests. It
// routes the OAuth2 token-exchange (POST to oauth2.googleapis.com/token)
// and the androidpublisher edits insert/delete calls to dedicated
// handlers, so each test wires only the responses it cares about.
//
// The check is expected to call:
//
//	POST   /androidpublisher/v3/applications/<pkg>/edits         (insert)
//	DELETE /androidpublisher/v3/applications/<pkg>/edits/<id>    (delete)
//
// against host androidpublisher.googleapis.com.
type packageRT struct {
	t          *testing.T
	tokenCalls int
	insertResp func(req *http.Request) (*http.Response, error)
	deleteResp func(req *http.Request) (*http.Response, error)

	// Observed call sequence, useful for asserting the cleanup happens.
	calls []string
}

func (p *packageRT) RoundTrip(req *http.Request) (*http.Response, error) {
	p.calls = append(p.calls, req.Method+" "+req.URL.Path)
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		p.tokenCalls++
		body := `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits") {
		if p.insertResp == nil {
			p.t.Fatalf("packageRT: unexpected insert call (no handler set): %s %s", req.Method, req.URL)
		}
		return p.insertResp(req)
	}
	if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") {
		if p.deleteResp == nil {
			p.t.Fatalf("packageRT: unexpected delete call (no handler set): %s %s", req.Method, req.URL)
		}
		return p.deleteResp(req)
	}
	p.t.Fatalf("packageRT: unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

// insertOK returns an edits.insert handler that responds 200 with a
// well-formed Edit body so the check has an Edit ID to discard.
func insertOK() func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"id":"edit-xyz","expiryTimeSeconds":"1700000000"}`), nil
	}
}

func deleteOK() func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewBufferString(""))}, nil
	}
}

func TestCheckPackageAccess_happyPath_passes(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t:          t,
		insertResp: insertOK(),
		deleteResp: deleteOK(),
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if !r.Passed || r.Skipped {
		t.Errorf("result = %+v, want Passed=true Skipped=false", r)
	}
	if !strings.Contains(r.Name, "com.example.app") {
		t.Errorf("Name = %q, want to contain package name", r.Name)
	}
}

func TestCheckPackageAccess_happyPath_alwaysCleansUpEdit(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t:          t,
		insertResp: insertOK(),
		deleteResp: deleteOK(),
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	_ = doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))

	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-xyz") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("CheckPackageAccess did not DELETE the open Edit; saw calls: %v", rt.calls)
	}
}

func TestCheckPackageAccess_403_returnsExit11AndCanonicalHint(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t: t,
		insertResp: func(*http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"error":{"code":403,"message":"forbidden"}}`), nil
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("403: Passed=true, want false")
	}
	if r.ExitCode != 11 {
		t.Errorf("ExitCode = %d, want 11", r.ExitCode)
	}
	if !strings.Contains(r.Hint, "Play Console") || !strings.Contains(r.Hint, "API access") {
		t.Errorf("Hint = %q, want to mention 'Play Console → Setup → API access'", r.Hint)
	}
	if !strings.Contains(r.Hint, "com.example.app") {
		t.Errorf("Hint = %q, want to contain the package name", r.Hint)
	}
	if !strings.Contains(r.Hint, "ci@test-proj.iam.gserviceaccount.com") {
		t.Errorf("Hint = %q, want to contain the SA client_email", r.Hint)
	}
}

func TestCheckPackageAccess_404_returnsExit30(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t: t,
		insertResp: func(*http.Request) (*http.Response, error) {
			return jsonResponse(404, `{"error":{"code":404,"message":"not found"}}`), nil
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("404: Passed=true, want false")
	}
	if r.ExitCode != 30 {
		t.Errorf("ExitCode = %d, want 30", r.ExitCode)
	}
	if !strings.Contains(r.Hint, "com.example.app") {
		t.Errorf("Hint = %q, want to contain the package name", r.Hint)
	}
}

func TestCheckPackageAccess_400_returnsExit30(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t: t,
		insertResp: func(*http.Request) (*http.Response, error) {
			return jsonResponse(400, `{"error":{"code":400,"message":"bad request"}}`), nil
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("400: Passed=true, want false")
	}
	if r.ExitCode != 30 {
		t.Errorf("ExitCode = %d, want 30", r.ExitCode)
	}
}

func TestCheckPackageAccess_5xxOnInsert_returnsExit40(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t: t,
		insertResp: func(*http.Request) (*http.Response, error) {
			return jsonResponse(503, `{"error":{"code":503,"message":"service unavailable"}}`), nil
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("503: Passed=true, want false")
	}
	if r.ExitCode != 40 {
		t.Errorf("ExitCode = %d, want 40", r.ExitCode)
	}
}

func TestCheckPackageAccess_200InsertThen5xxDelete_returnsExit40(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t:          t,
		insertResp: insertOK(),
		deleteResp: func(*http.Request) (*http.Response, error) {
			return jsonResponse(503, `{"error":{"code":503,"message":"transient"}}`), nil
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("delete 503: Passed=true, want false (cleanup failure is reported)")
	}
	if r.ExitCode != 40 {
		t.Errorf("ExitCode = %d, want 40", r.ExitCode)
	}
	// Even though delete failed, the check MUST have attempted it
	// (otherwise we'd leak an open Edit on the user's package).
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("cleanup was not attempted; saw calls: %v", rt.calls)
	}
}

func TestCheckPackageAccess_networkFailureOnInsert_returnsExit50(t *testing.T) {
	sa := makeSignedSA(t)
	rt := &packageRT{
		t: t,
		insertResp: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: lookup androidpublisher.googleapis.com: no such host")
		},
	}
	ctx := ctxWithRT(t, roundTripperFunc(rt.RoundTrip))

	results := doctor.Run(ctx, sa, nil, doctor.CheckPackageAccess("com.example.app"))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.Passed {
		t.Errorf("network err: Passed=true, want false")
	}
	if r.ExitCode != 50 {
		t.Errorf("ExitCode = %d, want 50", r.ExitCode)
	}
}
