package resolver_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

const fakeSAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func newResolver(t *testing.T) (*resolver.Resolver, *config.Config, keystore.Backend) {
	t.Helper()
	// Always clear the credential env vars so tests are hermetic regardless of
	// what the dev's shell has exported. Individual tests opt back in with
	// t.Setenv when they want to exercise the env-driven layers.
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	cfg := &config.Config{}
	be := keystore.NewFileBackend(t.TempDir())
	return resolver.New(cfg, be), cfg, be
}

func TestResolve_returnsActiveAccountCredential(t *testing.T) {
	r, cfg, be := newResolver(t)

	cfg.AddAccount("ci")
	if err := cfg.SetActive("ci"); err != nil {
		t.Fatal(err)
	}
	if err := be.Save("ci", []byte(fakeSAJSON)); err != nil {
		t.Fatal(err)
	}

	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_noSource_returnsErrNoSource(t *testing.T) {
	r, _, _ := newResolver(t)
	_, err := r.Resolve(resolver.Inputs{})
	if !errors.Is(err, resolver.ErrNoSource) {
		t.Errorf("Resolve (empty inputs, empty config) = %v, want ErrNoSource", err)
	}
}

func TestResolve_serviceAccountFlag_inlineJSON(t *testing.T) {
	r, _, _ := newResolver(t)

	sa, err := r.Resolve(resolver.Inputs{ServiceAccountFlag: fakeSAJSON})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_serviceAccountFlag_path(t *testing.T) {
	r, _, _ := newResolver(t)
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte(fakeSAJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	sa, err := r.Resolve(resolver.Inputs{ServiceAccountFlag: path})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_accountFlag_picksStoredAccount(t *testing.T) {
	r, cfg, be := newResolver(t)
	cfg.AddAccount("other")
	if err := be.Save("other", []byte(fakeSAJSON)); err != nil {
		t.Fatal(err)
	}

	sa, err := r.Resolve(resolver.Inputs{AccountFlag: "other"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_envServiceAccount_inlineJSON(t *testing.T) {
	r, _, _ := newResolver(t)
	t.Setenv("GPLAY_SERVICE_ACCOUNT", fakeSAJSON)
	t.Setenv("GPLAY_ACCOUNT", "")

	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_envServiceAccount_path(t *testing.T) {
	r, _, _ := newResolver(t)
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte(fakeSAJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", path)
	t.Setenv("GPLAY_ACCOUNT", "")

	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_envAccount_picksStoredAccount(t *testing.T) {
	r, cfg, be := newResolver(t)
	cfg.AddAccount("envchosen")
	if err := be.Save("envchosen", []byte(fakeSAJSON)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", "")
	t.Setenv("GPLAY_ACCOUNT", "envchosen")

	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_noSource_errorHint_mentionsLoginAndEnv(t *testing.T) {
	r, _, _ := newResolver(t)
	t.Setenv("GPLAY_SERVICE_ACCOUNT", "")
	t.Setenv("GPLAY_ACCOUNT", "")

	_, err := r.Resolve(resolver.Inputs{})
	if err == nil {
		t.Fatal("Resolve: expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"gplay auth login", "GPLAY_SERVICE_ACCOUNT", "--service-account"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// saJSONWithEmail returns a copy of fakeSAJSON with client_email replaced by
// the given value. Useful for precedence tests where each source carries a
// distinguishable email so we can verify which one won.
func saJSONWithEmail(email string) string {
	return strings.Replace(fakeSAJSON, "ci@p.iam.gserviceaccount.com", email, 1)
}

func TestResolve_precedence_flagServiceAccount_winsAll(t *testing.T) {
	r, cfg, be := newResolver(t)

	// Layer 2 candidate
	cfg.AddAccount("acct-flag")
	if err := be.Save("acct-flag", []byte(saJSONWithEmail("acctflag@x"))); err != nil {
		t.Fatal(err)
	}
	// Layer 4 candidate
	cfg.AddAccount("acct-env")
	if err := be.Save("acct-env", []byte(saJSONWithEmail("acctenv@x"))); err != nil {
		t.Fatal(err)
	}
	// Layer 5 candidate (active)
	cfg.AddAccount("acct-active")
	if err := be.Save("acct-active", []byte(saJSONWithEmail("active@x"))); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActive("acct-active"); err != nil {
		t.Fatal(err)
	}
	// Layer 3 candidate
	t.Setenv("GPLAY_SERVICE_ACCOUNT", saJSONWithEmail("envsa@x"))
	// Layer 4 candidate
	t.Setenv("GPLAY_ACCOUNT", "acct-env")

	// Layer 1: flag SA wins over everything else.
	sa, err := r.Resolve(resolver.Inputs{
		ServiceAccountFlag: saJSONWithEmail("flagsa@x"),
		AccountFlag:        "acct-flag",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "flagsa@x" {
		t.Errorf("winner = %q, want %q (flag SA layer 1)", sa.ClientEmail, "flagsa@x")
	}
}

func TestResolve_precedence_accountFlag_beatsEnvAndActive(t *testing.T) {
	r, cfg, be := newResolver(t)

	cfg.AddAccount("acct-flag")
	if err := be.Save("acct-flag", []byte(saJSONWithEmail("acctflag@x"))); err != nil {
		t.Fatal(err)
	}
	cfg.AddAccount("acct-env")
	if err := be.Save("acct-env", []byte(saJSONWithEmail("acctenv@x"))); err != nil {
		t.Fatal(err)
	}
	cfg.AddAccount("acct-active")
	if err := be.Save("acct-active", []byte(saJSONWithEmail("active@x"))); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActive("acct-active"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", saJSONWithEmail("envsa@x"))
	t.Setenv("GPLAY_ACCOUNT", "acct-env")

	// Layer 2: --account beats env and active (but no --service-account).
	sa, err := r.Resolve(resolver.Inputs{AccountFlag: "acct-flag"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "acctflag@x" {
		t.Errorf("winner = %q, want %q (--account layer 2)", sa.ClientEmail, "acctflag@x")
	}
}

func TestResolve_precedence_envServiceAccount_beatsEnvAccountAndActive(t *testing.T) {
	r, cfg, be := newResolver(t)

	cfg.AddAccount("acct-env")
	if err := be.Save("acct-env", []byte(saJSONWithEmail("acctenv@x"))); err != nil {
		t.Fatal(err)
	}
	cfg.AddAccount("acct-active")
	if err := be.Save("acct-active", []byte(saJSONWithEmail("active@x"))); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActive("acct-active"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", saJSONWithEmail("envsa@x"))
	t.Setenv("GPLAY_ACCOUNT", "acct-env")

	// Layer 3: GPLAY_SERVICE_ACCOUNT beats GPLAY_ACCOUNT and active.
	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "envsa@x" {
		t.Errorf("winner = %q, want %q (GPLAY_SERVICE_ACCOUNT layer 3)", sa.ClientEmail, "envsa@x")
	}
}

func TestResolve_envServiceAccount_inlineJSON_withLeadingWhitespace(t *testing.T) {
	r, _, _ := newResolver(t)
	// Leading whitespace + newline must still be detected as inline JSON.
	t.Setenv("GPLAY_SERVICE_ACCOUNT", "  \n\t"+fakeSAJSON)
	t.Setenv("GPLAY_ACCOUNT", "")

	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_envServiceAccount_nonexistentPath_returnsError(t *testing.T) {
	r, _, _ := newResolver(t)
	t.Setenv("GPLAY_SERVICE_ACCOUNT", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("GPLAY_ACCOUNT", "")

	_, err := r.Resolve(resolver.Inputs{})
	if err == nil {
		t.Fatal("Resolve: expected error for nonexistent path, got nil")
	}
	// Don't pin the exact wrapping; just make sure it doesn't fall through
	// to ErrNoSource (which would mean we silently skipped layer 3).
	if errors.Is(err, resolver.ErrNoSource) {
		t.Errorf("Resolve fell through to ErrNoSource; layer 3 should fail loudly when set: %v", err)
	}
}

func TestResolve_emptyEnvVars_areIgnored(t *testing.T) {
	r, cfg, be := newResolver(t)
	cfg.AddAccount("active")
	if err := be.Save("active", []byte(fakeSAJSON)); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActive("active"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", "")
	t.Setenv("GPLAY_ACCOUNT", "")

	// Empty envs must behave identically to unset — fall through to layer 5.
	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolve_precedence_envAccount_beatsActive(t *testing.T) {
	r, cfg, be := newResolver(t)

	cfg.AddAccount("acct-env")
	if err := be.Save("acct-env", []byte(saJSONWithEmail("acctenv@x"))); err != nil {
		t.Fatal(err)
	}
	cfg.AddAccount("acct-active")
	if err := be.Save("acct-active", []byte(saJSONWithEmail("active@x"))); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActive("acct-active"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPLAY_SERVICE_ACCOUNT", "")
	t.Setenv("GPLAY_ACCOUNT", "acct-env")

	// Layer 4: GPLAY_ACCOUNT beats active.
	sa, err := r.Resolve(resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "acctenv@x" {
		t.Errorf("winner = %q, want %q (GPLAY_ACCOUNT layer 4)", sa.ClientEmail, "acctenv@x")
	}
}
