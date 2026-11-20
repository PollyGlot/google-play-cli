package addcmd_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
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
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func probeFake(editID string, insertStatusByPkg map[string]int) *testkit.Fake {
	return testkit.NewFake(
		func(c testkit.Call) (int, string, bool) {
			if c.Method != http.MethodPost || !strings.HasSuffix(c.Path, "/edits") {
				return 0, "", false
			}
			if status, bad := insertStatusByPkg[pkgFromPath(c.Path)]; bad {
				return status, fmt.Sprintf(`{"error":{"code":%d,"message":"stub"}}`, status), true
			}
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, editID), true
		},
		func(c testkit.Call) (int, string, bool) {
			return http.StatusNoContent, "", c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/")
		},
	)
}

func pkgFromPath(path string) string {
	const marker = "/applications/"
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
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

func failOnCall(t *testing.T) http.RoundTripper {
	t.Helper()
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("--no-verify must not make HTTP calls, but saw: %s %s", req.Method, req.URL)
		return nil, nil
	})
}

func TestRun_noVerify_skipsAPIAndRecordsAnyway(t *testing.T) {
	rc := newRC(t, failOnCall(t))

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

func TestRun_invalidPackage_rejectsClientSide(t *testing.T) {
	rc := newRC(t, failOnCall(t))

	_, err := addcmd.Run(rc, addcmd.Input{Packages: []string{"notapackage"}})
	if err == nil {
		t.Fatal("Run: expected validation error for missing dot, got nil")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For = %d, want 20 (client-side validation); err = %v", got, err)
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if registry.Has(g.Accounts, "playci", "notapackage") {
		t.Errorf("invalid package was persisted; accounts=%+v", g.Accounts)
	}
}

func TestRun_persistsUnderAccountThatActuallyRanProbe(t *testing.T) {
	rc := newRC(t, probeFake("edit-misattrib", nil))

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, config.OSFS{}, rc.ConfigPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	g.AddAccount("altname")
	if err := g.Save(rc.Ctx, config.OSFS{}, rc.ConfigPath); err != nil {
		t.Fatalf("Save seed: %v", err)
	}

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

func TestRun_accountNotInGlobal_refusesBeforeProbe(t *testing.T) {
	rc := newRC(t, failOnCall(t))
	rc.AccountName = "ghost"

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

func TestRun_apiError_doesNotPersistPackage(t *testing.T) {
	rc := newRC(t, probeFake("edit-x", map[string]int{"com.example.myapp": http.StatusForbidden}))

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

func TestRun_happyPath_validatesAndPersists(t *testing.T) {
	fake := probeFake("edit-add", nil)
	rc := newRC(t, fake)

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
	if fake.TokenExchanges() == 0 {
		t.Errorf("expected /token exchange; calls=%+v", fake.Calls())
	}
}

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
