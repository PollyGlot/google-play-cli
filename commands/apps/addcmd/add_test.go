// Package addcmd_test exercises `gplay apps add` at the kernel level:
// a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// commands/releases/upload_test pattern so the seams stay consistent
// across write-side commands.
package addcmd_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/apps/addcmd"
	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// validateRT terminates the OAuth2 /token exchange so token.Source
// produces a usable Bearer token, then routes the edits.insert and
// edits.delete pair that edits.Validate makes. Tests can pre-set
// insertStatus / insertBody to simulate API failures.
type validateRT struct {
	t            *testing.T
	editID       string
	insertStatus int    // 0 means "default 200 with editID"
	insertBody   string // raw response body to return; empty falls back to a default per status

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *validateRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		if r.insertStatus != 0 {
			body := r.insertBody
			if body == "" {
				body = fmt.Sprintf(`{"error":{"code":%d,"message":"stub"}}`, r.insertStatus)
			}
			return jsonResp(r.insertStatus, body), nil
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("validateRT: unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// signedSAJSON generates a service-account JSON whose private_key is a
// fresh RSA key so the oauth2 library can sign the exchange JWT in
// tests. Mirrors the helper in commands/releases/upload_test.go.
func signedSAJSON(t *testing.T) []byte {
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

// newRC builds a RunContext with a parsed Account, the RoundTripper
// installed as the oauth2.HTTPClient seam, a fresh global config seeded
// with one Account named "playci" (active), and a temp ConfigPath the
// command can write back to.
func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})

	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	g := &config.Global{Accounts: []config.Account{{Name: "playci", Active: true}}}
	if err := g.Save(ctx, config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	boot := kernel.Boot{ConfigPath: cfgPath}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{})
	rc.Account = sa
	rc.AccountName = "playci"
	rc.Resolved = &config.Resolved{
		ConfigAccount: "playci",
		Accounts:      g.Accounts,
		GlobalPath:    cfgPath,
	}
	rc.ConfigPath = cfgPath
	return rc
}

// failOnCallRT fails the test on any HTTP call. Tests pass it through
// the oauth2.HTTPClient seam to prove --no-verify never hits the wire.
type failOnCallRT struct{ t *testing.T }

func (r *failOnCallRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Fatalf("--no-verify must not make HTTP calls, but saw: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestRun_noVerify_skipsAPIAndRecordsAnyway asserts the --no-verify
// escape hatch: zero HTTP traffic but the package is still persisted
// under the active Account. The RoundTripper is the assertion: any
// network call would fail the test.
func TestRun_noVerify_skipsAPIAndRecordsAnyway(t *testing.T) {
	rt := &failOnCallRT{t: t}
	rc := newRC(t, rt)

	if _, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.myapp"}, NoVerify: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.myapp") {
		t.Errorf("package not registered under --no-verify; accounts=%+v", g.Accounts)
	}
}

// TestRun_invalidPackage_rejectsClientSide asserts a missing-dot
// package name is rejected before any HTTP traffic. The error must
// carry exit code 20 (client-side validation) so CI can distinguish
// "user mistyped the package" from "API said no".
func TestRun_invalidPackage_rejectsClientSide(t *testing.T) {
	rt := &failOnCallRT{t: t}
	rc := newRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"notapackage"}})
	if err == nil {
		t.Fatal("Run: expected validation error for missing dot, got nil")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For = %d, want 20 (client-side validation); err = %v", got, err)
	}

	// The registry must be untouched.
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if registry.Has(g.Accounts, "playci", "notapackage") {
		t.Errorf("invalid package was persisted; accounts=%+v", g.Accounts)
	}
}

// TestRun_persistsUnderAccountThatActuallyRanProbe asserts the
// account-misattribution fix: when --account / GPLAY_ACCOUNT pick a
// credential other than the on-disk Active, the package must be
// registered under THAT Account's name, not under the cascade's
// ConfigAccount snapshot. The pre-fix code did the opposite, silently
// recording the (Account, Package) pair under the wrong identity.
func TestRun_persistsUnderAccountThatActuallyRanProbe(t *testing.T) {
	rt := &validateRT{t: t, editID: "edit-misattrib"}
	rc := newRC(t, rt)

	// Seed a second Account in the global so registry.Add can find it.
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	g.AddAccount("altname")
	if err := g.Save(rc.Ctx, config.OSFS{}, rc.ConfigPath); err != nil {
		t.Fatalf("Save seed: %v", err)
	}

	// Simulate --account altname overriding the cascade: AccountName
	// is the resolver's choice, ConfigAccount stays "playci" (the
	// global Active flag).
	rc.AccountName = "altname"

	if _, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.app"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g, err = config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "altname", "com.example.app") {
		t.Errorf("package not registered under altname; accounts=%+v", g.Accounts)
	}
	if registry.Has(g.Accounts, "playci", "com.example.app") {
		t.Errorf("package wrongly registered under playci (ConfigAccount cascade snapshot); accounts=%+v", g.Accounts)
	}
}

// TestRun_accountNotInGlobal_refusesBeforeProbe asserts the pre-flight
// check: if rc.AccountName names an Account that isn't in the global
// config (keystore-only, or a stale local override), apps add must
// refuse client-side without burning a Google round-trip. The
// failOnCallRT is the assertion mechanism.
func TestRun_accountNotInGlobal_refusesBeforeProbe(t *testing.T) {
	rt := &failOnCallRT{t: t}
	rc := newRC(t, rt)
	rc.AccountName = "ghost" // not in g.Accounts (newRC seeds only "playci")

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.app"}})
	if err == nil {
		t.Fatal("Run: expected error for keystore-only account, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing account; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "gplay auth login") {
		t.Errorf("error should point at the resolution path; got %q", err.Error())
	}
}

// TestRun_apiError_doesNotPersistPackage asserts the most important
// safety property of `apps add`: a failed validate (here a 403: SA
// not invited on the app) must NOT add the package to the registry.
// Persisting on failure would defeat the whole purpose of validate-
// by-default: users would end up with a registry full of packages
// the credential cannot reach.
func TestRun_apiError_doesNotPersistPackage(t *testing.T) {
	rt := &validateRT{t: t, editID: "edit-x", insertStatus: http.StatusForbidden}
	rc := newRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.myapp"}})
	if err == nil {
		t.Fatal("Run: expected error from 403 insert, got nil")
	}
	if got := exit.For(err); got != 11 {
		t.Errorf("exit.For = %d, want 11 (authorization); err = %v", got, err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if registry.Has(g.Accounts, "playci", "com.example.myapp") {
		t.Errorf("package was persisted despite API failure; accounts=%+v", g.Accounts)
	}
}

// TestRun_happyPath_validatesAndPersists is the tracer bullet: a real
// /token exchange + edits.insert + edits.delete round trip, then the
// global config carries the package under the active Account.
func TestRun_happyPath_validatesAndPersists(t *testing.T) {
	rt := &validateRT{t: t, editID: "edit-add"}
	rc := newRC(t, rt)

	if _, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.myapp"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if !registry.Has(g.Accounts, "playci", "com.example.myapp") {
		t.Errorf("package not registered after happy path; accounts=%+v", g.Accounts)
	}
	if rt.tokenHits == 0 {
		t.Errorf("expected /token exchange; calls=%v", rt.calls)
	}
}

// TestRun_malformedInlineCredential_exits10NotRegistered drives the
// production lazy path (kernel.Run → buildRunContext) with a malformed
// inline --service-account. The AccountName == "" branch must surface the
// invalid-credential error (exit 10, the real cause) instead of the
// misdirecting "run gplay auth login" authError, and the package must NOT
// be registered under the cascade Account (#180, ADR-0020).
func TestRun_malformedInlineCredential_exits10NotRegistered(t *testing.T) {
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	g := &config.Global{Accounts: []config.Account{{Name: "playci", Active: true}}}
	if err := g.Save(context.Background(), config.OSFS{}, cfgPath); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	boot := kernel.Boot{ConfigPath: cfgPath, KeystoreRoot: filepath.Join(root, "accounts")}
	in := kernel.Inputs{Resolver: resolver.Inputs{ServiceAccountFlag: "{ not valid json"}}
	runErr := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
		return addcmd.Run(rc, addcmd.Input{Packages: []string{"com.example.app"}, NoVerify: true})
	})
	if runErr == nil {
		t.Fatal("apps add with a malformed inline credential = nil, want exit 10")
	}
	if got := exit.For(runErr); got != 10 {
		t.Errorf("exit.For = %d, want 10; err=%v", got, runErr)
	}
	if !strings.Contains(runErr.Error(), "could not read credential") {
		t.Errorf("error should name the real cause; got %q", runErr.Error())
	}
	// Lock in the cause-preservation contract (%w): the wrapped JSON parse
	// error survives, not just the wrapper prefix.
	if !strings.Contains(runErr.Error(), "invalid character") {
		t.Errorf("error should preserve the underlying JSON parse cause; got %q", runErr.Error())
	}
	g2, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if registry.Has(g2.Accounts, "playci", "com.example.app") {
		t.Error("a malformed inline credential must not register the package under the cascade Account")
	}
}
