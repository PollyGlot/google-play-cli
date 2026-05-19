package login_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

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
	root := t.TempDir()
	opts := login.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
	}
	var stdout, stderr bytes.Buffer
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	return &stdout, &stderr, opts
}

func TestLogin_validSA_persistsAndActivates(t *testing.T) {
	saPath := writeSA(t, validSAJSON)
	_, stderr, opts := newCmd(t)

	cmd := login.NewCommand(opts)
	cmd.SetArgs([]string{"--service-account", saPath})
	if err := cmd.Execute(); err != nil {
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
	_, _, opts := newCmd(t)

	cmd := login.NewCommand(opts)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--service-account", saPath})
	err := cmd.Execute()
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

	_, _, opts := newCmd(t)

	// First login
	path1 := writeSA(t, v1)
	cmd := login.NewCommand(opts)
	cmd.SetArgs([]string{"--service-account", path1, "--name", "shared"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	// Re-login with same --name but different content
	path2 := writeSA(t, v2)
	cmd = login.NewCommand(opts)
	cmd.SetArgs([]string{"--service-account", path2, "--name", "shared"})
	if err := cmd.Execute(); err != nil {
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
