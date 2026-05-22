package logout_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/logout"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestRun_pureBusiness drives logout.Run with a hand-built RunContext.
// Run mutates the on-disk config + the keystore backend in rc.Keystore.
func TestRun_pureBusiness(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta")
	be, _, err := keystore.Select(keystore.SelectOptions{Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Keystore = be

	r, err := logout.Run(rc, logout.Input{Name: "beta"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r != nil {
		t.Errorf("logout.Run returned a Renderable; want nil")
	}
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	for _, a := range cfg.Accounts {
		if a.Name == "beta" {
			t.Errorf("beta still in registry: %+v", cfg.Accounts)
		}
	}
}

// fakeKeyring is the same minimal double the login/status tests use:
// in-process map keyed by service + user, no real OS keystore.
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

func newBoot(t *testing.T, kr keystore.KeyringAPI) kernel.Boot {
	t.Helper()
	root := t.TempDir()
	return kernel.Boot{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      kr,
	}
}

// seed installs n accounts; the first one is marked active.
func seed(t *testing.T, boot kernel.Boot, names ...string) {
	t.Helper()
	be, _, err := keystore.Select(keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("keystore.Select: %v", err)
	}
	cfg := &config.Global{}
	for _, n := range names {
		cfg.AddAccount(n)
		if err := be.Save(n, []byte(`{"client_email":"`+n+`@x"}`)); err != nil {
			t.Fatalf("be.Save(%s): %v", n, err)
		}
	}
	if len(names) > 0 {
		if err := cfg.SetActive(names[0]); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}
	// Reset Select so subsequent runCmd picks up the fake again.
}

func runCmd(t *testing.T, boot kernel.Boot, stdout, stderr *bytes.Buffer, args ...string) error {
	t.Helper()
	boot.Stdout = stdout
	boot.Stderr = stderr
	sub := logout.NewCommand(boot)
	root := &cobra.Command{Use: "gplay"}
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.AddCommand(sub)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"logout"}, args...))
	return root.Execute()
}

func TestLogout_existingAccount_clearsBothStores(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta")

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "beta"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Keystore: beta is gone.
	be, _, err := keystore.Select(keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if _, err := be.Load("beta"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("be.Load(beta) = %v, want ErrNotFound", err)
	}
	// Config: beta is gone.
	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	for _, a := range cfg.Accounts {
		if a.Name == "beta" {
			t.Errorf("beta still in config: %+v", cfg.Accounts)
		}
	}
}

func TestLogout_unknownAccount_exitsWithErrUnknown(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha")

	var stdout, stderr bytes.Buffer
	err := runCmd(t, boot, &stdout, &stderr, "ghost")
	if !errors.Is(err, logout.ErrUnknownAccount) {
		t.Errorf("err = %v, want ErrUnknownAccount", err)
	}
	// Hint mentions the known account.
	if !strings.Contains(stderr.String(), "alpha") {
		t.Errorf("stderr should list known accounts; got %q", stderr.String())
	}
}

func TestLogout_activeAccount_leavesRegistryWithoutActive(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta") // alpha is active (first one)

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "alpha"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if _, ok := cfg.Active(); ok {
		t.Errorf("Active() should be false after logging out of the active account")
	}
	// beta is still present (only alpha was removed).
	found := false
	for _, a := range cfg.Accounts {
		if a.Name == "beta" {
			found = true
		}
	}
	if !found {
		t.Errorf("beta should still be registered; got %+v", cfg.Accounts)
	}
}
