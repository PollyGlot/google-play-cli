package logout_test

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

	"github.com/PollyGlot/google-play-cli/commands/auth/logout"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

func TestRun_pureBusiness(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta")
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Keystore = be

	r, err := logout.Run(rc, logout.Input{Name: "beta", Confirm: true})
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

func TestRun_withoutConfirm_refusesExit3(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta")
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{Keyring: boot.Keyring, FileRoot: boot.KeystoreRoot})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	rc := kernel.NewForTest(context.Background(), boot, kernel.Inputs{})
	rc.Keystore = be

	_, err = logout.Run(rc, logout.Input{Name: "beta"})
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) {
		t.Fatalf("err = %v (%T), want *exit.SafetyFlagError", err, err)
	}
	if safety.ExitCode() != 3 {
		t.Errorf("ExitCode() = %d, want 3", safety.ExitCode())
	}
	if safety.Flag != "confirm" {
		t.Errorf("Flag = %q, want %q (feeds requires[] in the JSON envelope)", safety.Flag, "confirm")
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	var found bool
	for _, a := range cfg.Accounts {
		if a.Name == "beta" {
			found = true
		}
	}
	if !found {
		t.Errorf("refused logout removed %q from the registry: %+v", "beta", cfg.Accounts)
	}
}

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

func seed(t *testing.T, boot kernel.Boot, names ...string) {
	t.Helper()
	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("keystore.Select: %v", err)
	}
	cfg := &config.Global{}
	for _, n := range names {
		cfg.AddAccount(n)
		if err := be.Save(context.Background(), n, []byte(`{"client_email":"`+n+`@x"}`)); err != nil {
			t.Fatalf("be.Save(context.Background(), %s): %v", n, err)
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
	if err := runCmd(t, boot, &stdout, &stderr, "beta", "--confirm"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	be, _, err := keystore.Select(context.Background(), keystore.SelectOptions{
		Keyring:  boot.Keyring,
		FileRoot: boot.KeystoreRoot,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if _, err := be.Load(context.Background(), "beta"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("be.Load(context.Background(), beta) = %v, want ErrNotFound", err)
	}
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
	err := runCmd(t, boot, &stdout, &stderr, "ghost", "--confirm")
	if !errors.Is(err, logout.ErrUnknownAccount) {
		t.Errorf("err = %v, want ErrUnknownAccount", err)
	}
	if !strings.Contains(stderr.String(), "alpha") {
		t.Errorf("stderr should list known accounts; got %q", stderr.String())
	}
}

func TestLogout_activeAccount_leavesRegistryWithoutActive(t *testing.T) {
	boot := newBoot(t, newFakeKeyring(true))
	seed(t, boot, "alpha", "beta")

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "alpha", "--confirm"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cfg, err := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if _, ok := cfg.Active(); ok {
		t.Errorf("Active() should be false after logging out of the active account")
	}
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

func TestLogout_fileWrittenWithoutKeyring_removedOnceKeyringReachable(t *testing.T) {
	kr := newFakeKeyring(true)
	boot := newBoot(t, kr)
	seed(t, boot, "alpha", "beta")
	path := filepath.Join(boot.KeystoreRoot, "beta.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seed did not write %s: %v", path, err)
	}

	kr.unavailable = false
	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "beta", "--confirm"); err != nil {
		t.Fatalf("Execute: %v (stderr %q)", err, stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("plaintext credential still on disk at %s (stat err=%v)", path, err)
	}
	if !strings.Contains(stderr.String(), path) {
		t.Errorf("stderr = %q, want it to name the file store %s", stderr.String(), path)
	}
}

func TestLogout_bothStores_removesBothAndNamesThem(t *testing.T) {
	kr := newFakeKeyring(true)
	boot := newBoot(t, kr)
	seed(t, boot, "alpha", "beta")
	kr.unavailable = false
	if err := keystore.NewKeyringBackend(kr, keystore.KeyringService).Save(context.Background(), "beta", []byte(`{}`)); err != nil {
		t.Fatalf("keyring Save: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "beta", "--confirm"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := keystore.NewKeyringBackend(kr, keystore.KeyringService).Load(context.Background(), "beta"); !errors.Is(err, keystore.ErrNotFound) {
		t.Errorf("keyring Load after logout = %v, want ErrNotFound", err)
	}
	path := filepath.Join(boot.KeystoreRoot, "beta.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file copy still at %s", path)
	}
	for _, want := range []string{"OS keyring", path} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to name %q", stderr.String(), want)
		}
	}
}

func TestLogout_noStoredCredential_reachableKeyring_idempotent(t *testing.T) {
	kr := newFakeKeyring(false)
	boot := newBoot(t, kr)
	seed(t, boot, "alpha", "beta")
	if err := keystore.NewKeyringBackend(kr, keystore.KeyringService).Delete(context.Background(), "beta"); err != nil {
		t.Fatalf("pre-delete: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := runCmd(t, boot, &stdout, &stderr, "beta", "--confirm"); err != nil {
		t.Fatalf("Execute: %v, want success (idempotent logout)", err)
	}
	if !strings.Contains(stderr.String(), "no stored credential found, nothing to delete") {
		t.Errorf("stderr = %q, want the nothing-to-delete warning", stderr.String())
	}
	if strings.Contains(stderr.String(), "credential deleted from") {
		t.Errorf("stderr claims a deletion that did not happen: %q", stderr.String())
	}
	cfg, lerr := config.LoadGlobalOrEmpty(context.Background(), config.OSFS{}, boot.ConfigPath)
	if lerr != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", lerr)
	}
	for _, a := range cfg.Accounts {
		if a.Name == "beta" {
			t.Errorf("beta still registered: %+v", cfg.Accounts)
		}
	}
}

func TestLogout_keyringUnreachable_notInFile_warnsKeyMayRemain(t *testing.T) {
	kr := newFakeKeyring(true)
	boot := newBoot(t, kr)
	seed(t, boot, "alpha", "beta")
	if err := os.Remove(filepath.Join(boot.KeystoreRoot, "beta.json")); err != nil {
		t.Fatalf("remove file copy: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runCmd(t, boot, &stdout, &stderr, "beta", "--confirm")
	if err == nil || !strings.Contains(err.Error(), "OS keyring is unavailable") || !strings.Contains(err.Error(), "NOT deleted") {
		t.Fatalf("err = %v, want the keyring-unreachable failure", err)
	}
}
