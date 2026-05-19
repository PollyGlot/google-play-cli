package status_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

const validSAJSON = `{
  "type": "service_account",
  "project_id": "test-proj",
  "private_key": "-----BEGIN PRIVATE KEY-----\nX\n-----END PRIVATE KEY-----\n",
  "client_email": "playci@test-proj.iam.gserviceaccount.com",
  "token_uri": "https://oauth2.googleapis.com/token"
}`

func seedActiveAccount(t *testing.T) (status.Options, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	opts := status.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
	}
	var stdout, stderr bytes.Buffer
	opts.Stdout = &stdout
	opts.Stderr = &stderr

	be := keystore.NewFileBackend(opts.KeystoreRoot)
	if err := be.Save("playci", []byte(validSAJSON)); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.AddAccount("playci")
	if err := cfg.SetActive("playci"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		t.Fatal(err)
	}
	return opts, &stdout, &stderr
}

func TestStatus_table_showsNameEmailAndPath(t *testing.T) {
	opts, stdout, _ := seedActiveAccount(t)

	cmd := status.NewCommand(opts)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "playci") {
		t.Errorf("output missing account name; got %q", out)
	}
	if !strings.Contains(out, "playci@test-proj.iam.gserviceaccount.com") {
		t.Errorf("output missing client_email; got %q", out)
	}
	if !strings.Contains(out, filepath.Join(opts.KeystoreRoot, "playci.json")) {
		t.Errorf("output missing credential path; got %q", out)
	}
}

func TestStatus_jsonOutput_returnsStructuredPayload(t *testing.T) {
	opts, stdout, _ := seedActiveAccount(t)

	cmd := status.NewCommand(opts)
	cmd.SetArgs([]string{"--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Name        string `json:"name"`
		ClientEmail string `json:"client_email"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v (raw=%q)", err, stdout.String())
	}
	if payload.Name != "playci" {
		t.Errorf("json.name = %q, want playci", payload.Name)
	}
	if payload.ClientEmail != "playci@test-proj.iam.gserviceaccount.com" {
		t.Errorf("json.client_email = %q", payload.ClientEmail)
	}
	if payload.Path != filepath.Join(opts.KeystoreRoot, "playci.json") {
		t.Errorf("json.path = %q", payload.Path)
	}
}

func TestStatus_noActiveAccount_returnsErrNoActive(t *testing.T) {
	root := t.TempDir()
	opts := status.Options{
		ConfigPath:   filepath.Join(root, "config.json"),
		KeystoreRoot: filepath.Join(root, "accounts"),
	}
	var stdout, stderr bytes.Buffer
	opts.Stdout = &stdout
	opts.Stderr = &stderr

	cmd := status.NewCommand(opts)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute: expected error with no active account, got nil")
	}
	if !errors.Is(err, resolver.ErrNoActive) {
		t.Errorf("err = %v, want resolver.ErrNoActive", err)
	}
}
