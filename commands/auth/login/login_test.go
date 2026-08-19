package login_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestRun_pureBusiness drives login.Run with a hand-built RunContext.
func TestRun_pureBusiness(t *testing.T) {
	_, _, boot := newCmd(t)
	saPath := writeSA(t, validSAJSON)
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Keystore = be

	r, err := login.Run(rc, login.Input{SAPath: saPath, Activate: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r != nil {
		t.Errorf("login.Run returned a Renderable; want nil")
	}
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	a, ok := cfg.Active()
	if !ok || a.Name != "playci" {
		t.Errorf("active = %+v ok=%v, want playci active", a, ok)
	}
}

// TestRun_missingSAPath_isCLIError verifies the typed error mapping:
// the empty-input case must exit 2 (CLI misuse), not 1 (generic).
func TestRun_missingSAPath_isCLIError(t *testing.T) {
	_, _, boot := newCmd(t)
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	_, err := login.Run(rc, login.Input{Activate: true})
	if err == nil {
		t.Fatal("expected error for empty SAPath")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse)", got)
	}
}

// fakeKeyring is a configurable double for keystore.KeyringAPI. The login
// tests use it in "unavailable" mode to force the file backend path, plus
// one test in available mode to confirm the keyring path also persists.
type fakeKeyring struct {
	mu          sync.Mutex
	store       map[string]string
	unavailable bool
}

func newFakeKeyring(unavailable bool) *fakeKeyring {
	return &fakeKeyring{store: map[string]string{}, unavailable: unavailable}
}

func (f *fakeKeyring) key(service, user string) string { return service + "\x00" + user }

func (f *fakeKeyring) Set(service, user, pass string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return errors.New("keystore unavailable")
	}
	f.store[f.key(service, user)] = pass
	return nil
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return "", errors.New("keystore unavailable")
	}
	v, ok := f.store[f.key(service, user)]
	if !ok {
		return "", keystore.ErrKeyringNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return errors.New("keystore unavailable")
	}
	if _, ok := f.store[f.key(service, user)]; !ok {
		return keystore.ErrKeyringNotFound
	}
	delete(f.store, f.key(service, user))
	return nil
}

const validSAJSON = `{
  "type": "service_account",
  "project_id": "test-proj",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIBCG\n-----END PRIVATE KEY-----\n",
  "client_email": "playci@test-proj.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func writeSA(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write sa: %v", err)
	}
	return path
}

func newCmd(t *testing.T) (*bytes.Buffer, *bytes.Buffer, kernel.Boot) {
	t.Helper()
	root := t.TempDir()
	boot := kernel.Boot{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		// Force file backend in the standard tests so the existing
		// `keystore.NewFileBackend(boot.KeystoreRoot).Load(...)` reads keep
		// working. The keyring path has its own dedicated test below.
		Keyring: newFakeKeyring(true),
	}
	return &bytes.Buffer{}, &bytes.Buffer{}, boot
}

func runCmd(t *testing.T, boot kernel.Boot, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot.Stdout = stdout
	boot.Stderr = stderr
	// Wrap the subcommand in a minimal root that mirrors cmd/gplay/main.go:
	// --service-account is a persistent root flag (so login can read it
	// from cmd.Flags()) and --verbose is the standard verbosity persistent.
	sub := login.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.PersistentFlags().String("service-account", "", "")
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"login"}, args...))
	return root.Execute()
}

func TestLogin_validSA_persistsAndActivates(t *testing.T) {
	saPath := writeSA(t, validSAJSON)
	stdout, stderr, boot := newCmd(t)

	if err := runCmd(t, boot, stdout, stderr, "--service-account", saPath); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	a, ok := cfg.Active()
	if !ok {
		t.Fatal("no active account after login")
	}
	if a.Name != "playci" {
		t.Errorf("active account name = %q, want %q (derived from client_email)", a.Name, "playci")
	}

	be := keystore.NewFileBackend(boot.KeystoreRoot)
	data, err := be.Load(context.Background(), "playci")
	if err != nil {
		t.Fatalf("keystore.Load: %v", err)
	}
	if _, err := serviceaccount.Parse(data); err != nil {
		t.Errorf("persisted credential is not parseable: %v", err)
	}
	if !strings.Contains(stderr.String(), "playci") {
		t.Errorf("stderr success line should mention the registered name; got %q", stderr.String())
	}
}

func TestLogin_malformedSA_returnsTypedErrorWithFieldHint(t *testing.T) {
	body := strings.Replace(validSAJSON, `"playci@test-proj.iam.gserviceaccount.com"`, `""`, 1)
	saPath := writeSA(t, body)
	stdout, stderr, boot := newCmd(t)

	err := runCmd(t, boot, stdout, stderr, "--service-account", saPath)
	if err == nil {
		t.Fatal("Execute: expected error on missing client_email, got nil")
	}
	var fe *serviceaccount.MissingFieldError
	if !errors.As(err, &fe) || fe.Field != "client_email" {
		t.Errorf("Execute err = %v (%T); want *MissingFieldError{Field:\"client_email\"}", err, err)
	}
}

func TestLogin_reloginSameName_overwritesCleanly(t *testing.T) {
	v1 := strings.Replace(validSAJSON, `"-----BEGIN PRIVATE KEY-----\nMIIBCG\n-----END PRIVATE KEY-----\n"`, `"v1KEY"`, 1)
	v2 := strings.Replace(validSAJSON, `"-----BEGIN PRIVATE KEY-----\nMIIBCG\n-----END PRIVATE KEY-----\n"`, `"v2KEY"`, 1)

	stdout, stderr, boot := newCmd(t)

	path1 := writeSA(t, v1)
	if err := runCmd(t, boot, stdout, stderr, "--service-account", path1, "--name", "shared"); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	path2 := writeSA(t, v2)
	if err := runCmd(t, boot, stdout, stderr, "--service-account", path2, "--name", "shared"); err != nil {
		t.Fatalf("re-Execute: %v", err)
	}

	be := keystore.NewFileBackend(boot.KeystoreRoot)
	data, err := be.Load(context.Background(), "shared")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Contains(data, []byte("v2KEY")) {
		t.Errorf("after re-login, stored content does not contain v2KEY; got %s", data)
	}
	if bytes.Contains(data, []byte("v1KEY")) {
		t.Errorf("after re-login, stored content still contains v1KEY")
	}

	// And there should be exactly one entry
	names, _ := be.List(context.Background())
	if len(names) != 1 {
		t.Errorf("List = %v, want exactly one entry", names)
	}
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if len(cfg.Accounts) != 1 {
		t.Errorf("config Accounts = %v, want exactly one entry", cfg.Accounts)
	}
}

func TestLogin_activateFalse_onSecondAccount_keepsCurrentActive(t *testing.T) {
	_, _, boot := newCmd(t)
	saPath := writeSA(t, validSAJSON)

	// First login (default --activate=true).
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--service-account", saPath, "--name", "first"); err != nil {
		t.Fatalf("first login: %v", err)
	}

	// Second login with --activate=false must NOT shift the active flag.
	stdout.Reset()
	stderr.Reset()
	if err := runCmd(t, boot, &stdout, &stderr, "--service-account", saPath, "--name", "second", "--activate=false"); err != nil {
		t.Fatalf("second login: %v", err)
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("len(Accounts) = %d, want 2; cfg=%+v", len(cfg.Accounts), cfg)
	}
	active, ok := cfg.Active()
	if !ok {
		t.Fatal("Active() = false, want true (first should still be active)")
	}
	if active.Name != "first" {
		t.Errorf("active.Name = %q, want %q", active.Name, "first")
	}
}

func TestLogin_activateFalse_onFirstAccount_stillActivates(t *testing.T) {
	_, _, boot := newCmd(t)
	saPath := writeSA(t, validSAJSON)

	// Empty registry + --activate=false: the new Account is still
	// activated because the registry would otherwise be left without one.
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--service-account", saPath, "--name", "only", "--activate=false"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	active, ok := cfg.Active()
	if !ok {
		t.Fatal("Active() = false; first Account should always activate even with --activate=false")
	}
	if active.Name != "only" {
		t.Errorf("active.Name = %q, want %q", active.Name, "only")
	}
}

// TestLogin_developerID_recordedOnAccount asserts `auth login --developer-id`
// stores the id on the Account (ADR-0015 / #151 capture point 1) and mentions
// it on stderr.
func TestLogin_developerID_recordedOnAccount(t *testing.T) {
	saPath := writeSA(t, validSAJSON)
	stdout, stderr, boot := newCmd(t)

	if err := runCmd(t, boot, stdout, stderr, "--service-account", saPath, "--developer-id", "4900000000000000000"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	a, ok := cfg.Active()
	if !ok || a.DeveloperID != "4900000000000000000" {
		t.Errorf("active account = %+v, want DeveloperID recorded", a)
	}
	if !strings.Contains(stderr.String(), "4900000000000000000") {
		t.Errorf("stderr should mention the recorded developer-id; got %q", stderr.String())
	}
}

func TestLogin_keyringBackend_writesToKeyringAndNotFile(t *testing.T) {
	root := t.TempDir()
	fk := newFakeKeyring(false) // available
	boot := kernel.Boot{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      fk,
	}

	saPath := writeSA(t, validSAJSON)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "--service-account", saPath); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The credential must land in the keyring fake, not on disk.
	be := keystore.NewKeyringBackend(fk, keystore.KeyringService)
	if _, err := be.Load(context.Background(), "playci"); err != nil {
		t.Errorf("keyring backend: Load after login: %v", err)
	}
	// And the file accounts/ directory must not have been created.
	if _, err := os.Stat(boot.KeystoreRoot); !os.IsNotExist(err) {
		t.Errorf("expected no file at %s when keyring backend active; stat err=%v", boot.KeystoreRoot, err)
	}
}
