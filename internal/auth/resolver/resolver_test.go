package resolver_test

import (
	"context"
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

// saWithEmail returns fakeSAJSON with client_email replaced — useful in
// precedence cases where every layer needs a distinguishable winner.
func saWithEmail(email string) string {
	return strings.Replace(fakeSAJSON, "ci@p.iam.gserviceaccount.com", email, 1)
}

// setup returns a Deps wired to a fresh file-backed keystore (caller
// stashes any bytes it needs). The returned *config.Resolved is the
// layer-5 surface tests mutate to emulate "this account is active in
// config". Env vars are no longer touched — the resolver reads its
// EnvServiceAccount / EnvAccount from Inputs.
func setup(t *testing.T) (resolver.Deps, *config.Resolved, keystore.Backend) {
	t.Helper()
	resolved := &config.Resolved{}
	be := keystore.NewFileBackend(t.TempDir())
	return resolver.Deps{Resolved: resolved, Keystore: be}, resolved, be
}

// TestResolve_cascade walks the five precedence layers documented in
// DESIGN.md §1, one row per layer, each row asserting that the named
// layer wins when every higher-priority layer is unset. Layer-overlap
// (multiple sources set; highest wins) gets a separate group below.
func TestResolve_cascade(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, deps *resolver.Deps, resolved *config.Resolved, be keystore.Backend) resolver.Inputs
		want   string // expected ClientEmail
		wantOK bool
	}{
		{
			name: "layer1-flag-inline-json",
			setup: func(_ *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				return resolver.Inputs{ServiceAccountFlag: fakeSAJSON}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer1-flag-path",
			setup: func(t *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				p := filepath.Join(t.TempDir(), "sa.json")
				mustWrite(t, p, fakeSAJSON)
				return resolver.Inputs{ServiceAccountFlag: p}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer2-account-flag",
			setup: func(t *testing.T, _ *resolver.Deps, _ *config.Resolved, be keystore.Backend) resolver.Inputs {
				mustSave(t, be, "other", fakeSAJSON)
				return resolver.Inputs{AccountFlag: "other"}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer3-env-service-account-inline",
			setup: func(_ *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				return resolver.Inputs{EnvServiceAccount: fakeSAJSON}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer3-env-service-account-path",
			setup: func(t *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				p := filepath.Join(t.TempDir(), "sa.json")
				mustWrite(t, p, fakeSAJSON)
				return resolver.Inputs{EnvServiceAccount: p}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer3-env-inline-with-leading-whitespace",
			setup: func(_ *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				return resolver.Inputs{EnvServiceAccount: "  \n\t" + fakeSAJSON}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer4-env-account",
			setup: func(t *testing.T, _ *resolver.Deps, _ *config.Resolved, be keystore.Backend) resolver.Inputs {
				mustSave(t, be, "envchosen", fakeSAJSON)
				return resolver.Inputs{EnvAccount: "envchosen"}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "layer5-config-active",
			setup: func(t *testing.T, _ *resolver.Deps, resolved *config.Resolved, be keystore.Backend) resolver.Inputs {
				mustSave(t, be, "ci", fakeSAJSON)
				resolved.ConfigAccount = "ci"
				return resolver.Inputs{}
			},
			want:   "ci@p.iam.gserviceaccount.com",
			wantOK: true,
		},
		{
			name: "no-source",
			setup: func(_ *testing.T, _ *resolver.Deps, _ *config.Resolved, _ keystore.Backend) resolver.Inputs {
				return resolver.Inputs{}
			},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, resolved, be := setup(t)
			in := tc.setup(t, &deps, resolved, be)

			sa, err := resolver.Resolve(context.Background(), deps, in)
			if !tc.wantOK {
				if !errors.Is(err, resolver.ErrNoSource) {
					t.Errorf("err = %v, want ErrNoSource", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if sa.ClientEmail != tc.want {
				t.Errorf("ClientEmail = %q, want %q", sa.ClientEmail, tc.want)
			}
		})
	}
}

// TestResolve_precedence asserts the *order* of layers when several are
// set simultaneously — i.e. the layer that "wins" against every layer
// below it.
func TestResolve_precedence(t *testing.T) {
	type seed struct {
		flagSA   string
		flagAcct string
		envSA    string
		envAcct  string
	}
	allLayers := seed{
		flagSA:   saWithEmail("flagsa@x"),
		flagAcct: "acct-flag",
		envSA:    saWithEmail("envsa@x"),
		envAcct:  "acct-env",
	}
	cases := []struct {
		name string
		drop seed // layers set to zero for this case
		want string
	}{
		{
			name: "flag-service-account-wins-all",
			want: "flagsa@x",
		},
		{
			name: "account-flag-wins-when-no-flag-sa",
			drop: seed{flagSA: " "},
			want: "acctflag@x",
		},
		{
			name: "env-service-account-wins-when-no-flags",
			drop: seed{flagSA: " ", flagAcct: " "},
			want: "envsa@x",
		},
		{
			name: "env-account-wins-over-active",
			drop: seed{flagSA: " ", flagAcct: " ", envSA: " "},
			want: "acctenv@x",
		},
		{
			name: "active-wins-when-nothing-else",
			drop: seed{flagSA: " ", flagAcct: " ", envSA: " ", envAcct: " "},
			want: "active@x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, resolved, be := setup(t)
			// Seed every layer; drop overrides selectively below.
			mustSave(t, be, "acct-flag", saWithEmail("acctflag@x"))
			mustSave(t, be, "acct-env", saWithEmail("acctenv@x"))
			mustSave(t, be, "acct-active", saWithEmail("active@x"))
			resolved.ConfigAccount = "acct-active"

			// Apply drops — a non-empty drop field clears that layer.
			in := resolver.Inputs{
				ServiceAccountFlag: allLayers.flagSA,
				AccountFlag:        allLayers.flagAcct,
				EnvServiceAccount:  allLayers.envSA,
				EnvAccount:         allLayers.envAcct,
			}
			if tc.drop.flagSA != "" {
				in.ServiceAccountFlag = ""
			}
			if tc.drop.flagAcct != "" {
				in.AccountFlag = ""
			}
			if tc.drop.envSA != "" {
				in.EnvServiceAccount = ""
			}
			if tc.drop.envAcct != "" {
				in.EnvAccount = ""
			}

			sa, err := resolver.Resolve(context.Background(), deps, in)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if sa.ClientEmail != tc.want {
				t.Errorf("winner = %q, want %q", sa.ClientEmail, tc.want)
			}
		})
	}
}

// TestResolve_emptyEnvVars_areIgnored — an explicitly empty env var must
// fall through, not short-circuit a higher-priority layer.
func TestResolve_emptyEnvVars_areIgnored(t *testing.T) {
	deps, resolved, be := setup(t)
	mustSave(t, be, "active", fakeSAJSON)
	resolved.ConfigAccount = "active"

	sa, err := resolver.Resolve(context.Background(), deps, resolver.Inputs{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

// TestResolve_envServiceAccount_missingPath_failsLoudly — setting layer
// 3 to a path that doesn't exist must surface the underlying error
// rather than silently falling through to ErrNoSource.
func TestResolve_envServiceAccount_missingPath_failsLoudly(t *testing.T) {
	deps, _, _ := setup(t)
	missingPath := filepath.Join(t.TempDir(), "missing.json")

	_, err := resolver.Resolve(context.Background(), deps, resolver.Inputs{EnvServiceAccount: missingPath})
	if err == nil {
		t.Fatal("Resolve: expected error for nonexistent path, got nil")
	}
	if errors.Is(err, resolver.ErrNoSource) {
		t.Errorf("Resolve fell through to ErrNoSource; layer 3 should fail loudly when set: %v", err)
	}
}

// TestResolve_noSourceHint asserts the actionable hint stays in the
// ErrNoSource message — users land on this in the cold-start state.
func TestResolve_noSourceHint(t *testing.T) {
	deps, _, _ := setup(t)
	_, err := resolver.Resolve(context.Background(), deps, resolver.Inputs{})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"gplay auth login", "GPLAY_SERVICE_ACCOUNT", "--service-account"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %s", want, err.Error())
		}
	}
}

// explodingKeystore fails the test if any Backend method is called: it
// guards the promise that ResolveName never touches the keystore.
type explodingKeystore struct{ t *testing.T }

func (e explodingKeystore) Save(context.Context, string, []byte) error {
	e.t.Fatal("ResolveName must not call keystore.Save")
	return nil
}
func (e explodingKeystore) Load(context.Context, string) ([]byte, error) {
	e.t.Fatal("ResolveName must not call keystore.Load")
	return nil, nil
}
func (e explodingKeystore) Delete(context.Context, string) error {
	e.t.Fatal("ResolveName must not call keystore.Delete")
	return nil
}
func (e explodingKeystore) List(context.Context) ([]string, error) {
	e.t.Fatal("ResolveName must not call keystore.List")
	return nil, nil
}

func TestResolveName_precedenceAndKeystoreFree(t *testing.T) {
	deps := resolver.Deps{
		Resolved: &config.Resolved{ConfigAccount: "cascade"},
		Keystore: explodingKeystore{t: t}, // any access fails the test
	}
	cases := []struct {
		name string
		in   resolver.Inputs
		want string
	}{
		{"service-account flag → inline, no name", resolver.Inputs{ServiceAccountFlag: "/sa.json", AccountFlag: "acct"}, ""},
		{"account flag", resolver.Inputs{AccountFlag: "acct", EnvAccount: "env"}, "acct"},
		{"env service-account → inline, no name", resolver.Inputs{EnvServiceAccount: "{...}", EnvAccount: "env"}, ""},
		{"env account", resolver.Inputs{EnvAccount: "env"}, "env"},
		{"cascade ConfigAccount", resolver.Inputs{}, "cascade"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolver.ResolveName(deps, tc.in); got != tc.want {
				t.Errorf("ResolveName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveName_nilResolved_isEmpty(t *testing.T) {
	if got := resolver.ResolveName(resolver.Deps{}, resolver.Inputs{}); got != "" {
		t.Errorf("ResolveName with nil Resolved = %q, want \"\"", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustSave(t *testing.T, be keystore.Backend, name, content string) {
	t.Helper()
	if err := be.Save(context.Background(), name, []byte(content)); err != nil {
		t.Fatal(err)
	}
}
