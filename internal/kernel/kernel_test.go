package kernel_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

const fakeSAJSON = `{
  "type": "service_account",
  "project_id": "p",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXX\n-----END PRIVATE KEY-----\n",
  "client_email": "ci@p.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

// newBoot wires a Boot rooted at t.TempDir with a fake keyring that
// always fails its probe, so the file backend gets picked. Each test
// owns its tempdir.
func newBoot(t *testing.T) kernel.Boot {
	t.Helper()
	t.Setenv(resolver.EnvServiceAccount, "")
	t.Setenv(resolver.EnvAccount, "")
	root := t.TempDir()
	return kernel.Boot{
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
		Keyring:      unavailableKeyring{},
	}
}

// unavailableKeyring is a KeyringAPI whose probe always fails — Select
// then falls back to the file backend.
type unavailableKeyring struct{}

func (unavailableKeyring) Set(string, string, string) error { return errBoom }
func (unavailableKeyring) Get(string, string) (string, error) {
	return "", errBoom
}
func (unavailableKeyring) Delete(string, string) error { return errBoom }

var errBoom = stringError("keyring unavailable")

type stringError string

func (s stringError) Error() string { return string(s) }

func TestNew_populatesStreamsAndPaths(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)
	if rc.Stdout != boot.Stdout {
		t.Errorf("Stdout not wired from Boot")
	}
	if rc.Stderr != boot.Stderr {
		t.Errorf("Stderr not wired from Boot")
	}
	if rc.ConfigPath != boot.ConfigPath {
		t.Errorf("ConfigPath mismatch")
	}
	if rc.KeystoreRoot != boot.KeystoreRoot {
		t.Errorf("KeystoreRoot mismatch")
	}
}

func TestConfig_lazy_returnsResolved(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)
	resolved, err := rc.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if resolved == nil {
		t.Fatal("Config returned nil")
	}
}

func TestConfig_isCached(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)
	a, _ := rc.Config()
	b, _ := rc.Config()
	if a != b {
		t.Errorf("Config returned different pointers on 2nd call; lazy cache broken")
	}
}

func TestBackend_lazy_fallsBackToFile(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)
	be, label, err := rc.Backend()
	if err != nil {
		t.Fatalf("Backend: %v", err)
	}
	if be == nil {
		t.Fatal("Backend returned nil")
	}
	if label != keystore.BackendFile {
		t.Errorf("label = %q, want %q (probe failed → file backend)", label, keystore.BackendFile)
	}
}

func TestBackend_verbose_logsLabelOnce(t *testing.T) {
	boot := newBoot(t)
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	rc := kernel.New(context.Background(), boot, true)
	if _, _, err := rc.Backend(); err != nil {
		t.Fatalf("Backend: %v", err)
	}
	if _, _, err := rc.Backend(); err != nil {
		t.Fatalf("Backend: %v", err)
	}
	out := stderr.String()
	if c := strings.Count(out, "keystore: using"); c != 1 {
		t.Errorf("verbose backend label logged %d times, want 1; stderr=%q", c, out)
	}
}

func TestResolveAccount_fromConfigActive(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)

	// Seed the file backend + config so layer-5 resolves.
	be, _, err := rc.Backend()
	if err != nil {
		t.Fatalf("Backend: %v", err)
	}
	if err := be.Save("ci", []byte(fakeSAJSON)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg := &config.Global{}
	cfg.AddAccount("ci")
	if err := cfg.SetActive("ci"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := cfg.Save(context.Background(), config.OSFS{}, boot.ConfigPath); err != nil {
		t.Fatalf("cfg.Save: %v", err)
	}

	// Reset the lazy state and ask the kernel to resolve.
	rc = kernel.New(context.Background(), boot, false)
	sa, err := rc.ResolveAccount(resolver.Inputs{})
	if err != nil {
		t.Fatalf("ResolveAccount: %v", err)
	}
	if sa.ClientEmail != "ci@p.iam.gserviceaccount.com" {
		t.Errorf("ClientEmail = %q", sa.ClientEmail)
	}
}

func TestResolveAccount_noSource(t *testing.T) {
	boot := newBoot(t)
	rc := kernel.New(context.Background(), boot, false)
	_, err := rc.ResolveAccount(resolver.Inputs{})
	if err == nil {
		t.Fatal("expected error from empty cascade")
	}
}
