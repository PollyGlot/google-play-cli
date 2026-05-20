package login_test

import (
	"bytes"
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
)

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

func newCmd(t *testing.T) (*bytes.Buffer, *bytes.Buffer, login.Options) {
	t.Helper()
	keystore.ResetSelectForTest()
	root := t.TempDir()
	opts := login.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		// Force file backend in the standard tests so the existing
		// `keystore.NewFileBackend(opts.KeystoreRoot).Load(...)` reads keep
		// working. The keyring path has its own dedicated test below.
		Keyring: newFakeKeyring(true),
	}
	return &bytes.Buffer{}, &bytes.Buffer{}, opts
}

func runCmd(t *testing.T, opts login.Options, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	// Wrap the subcommand in a minimal root so the persistent --verbose
	// flag is in scope, matching the production wiring in cmd/gplay/main.go.
	sub := login.NewCommand(opts)
	root := &cobra.Command{Use: "gplay"}
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"login"}, args...))
	return root.Execute()
}

func TestLogin_validSA_persistsAndActivates(t *testing.T) {
	saPath := writeSA(t, validSAJSON)
	stdout, stderr, opts := newCmd(t)

	if err := runCmd(t, opts, stdout, stderr, "--service-account", saPath); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	a, ok := cfg.Active()
	if !ok {
		t.Fatal("no active account after login")
	}
	if a.Name != "playci" {
		t.Errorf("active account name = %q, want %q (derived from client_email)", a.Name, "playci")
	}

	be := keystore.NewFileBackend(opts.KeystoreRoot)
	data, err := be.Load("playci")
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
	stdout, stderr, opts := newCmd(t)

	err := runCmd(t, opts, stdout, stderr, "--service-account", saPath)
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

	stdout, stderr, opts := newCmd(t)

	path1 := writeSA(t, v1)
	if err := runCmd(t, opts, stdout, stderr, "--service-account", path1, "--name", "shared"); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	path2 := writeSA(t, v2)
	if err := runCmd(t, opts, stdout, stderr, "--service-account", path2, "--name", "shared"); err != nil {
		t.Fatalf("re-Execute: %v", err)
	}

	be := keystore.NewFileBackend(opts.KeystoreRoot)
	data, err := be.Load("shared")
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
	names, _ := be.List()
	if len(names) != 1 {
		t.Errorf("List = %v, want exactly one entry", names)
	}
	cfg, _ := config.LoadOrEmpty(opts.ConfigPath)
	if len(cfg.Accounts) != 1 {
		t.Errorf("config Accounts = %v, want exactly one entry", cfg.Accounts)
	}
}

func TestLogin_keyringBackend_writesToKeyringAndNotFile(t *testing.T) {
	keystore.ResetSelectForTest()
	root := t.TempDir()
	fk := newFakeKeyring(false) // available
	opts := login.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      fk,
	}

	saPath := writeSA(t, validSAJSON)
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, opts, &stdout, &stderr, "--service-account", saPath); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The credential must land in the keyring fake, not on disk.
	be := keystore.NewKeyringBackend(fk, keystore.KeyringService)
	if _, err := be.Load("playci"); err != nil {
		t.Errorf("keyring backend: Load after login: %v", err)
	}
	// And the file accounts/ directory must not have been created.
	if _, err := os.Stat(opts.KeystoreRoot); !os.IsNotExist(err) {
		t.Errorf("expected no file at %s when keyring backend active; stat err=%v", opts.KeystoreRoot, err)
	}
}
