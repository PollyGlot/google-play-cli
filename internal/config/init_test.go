package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

func TestInit_writesConfigJSON_withPackagePin(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(repo, home, "com.example.app"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfgPath := filepath.Join(repo, ".gplay", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	if !strings.Contains(string(data), `"package": "com.example.app"`) {
		t.Errorf("config.json does not contain pin; got %s", data)
	}
}

func TestInit_writesGitignore_excludingLocalAndEditFiles(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(repo, home, "com.example.app"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	giPath := filepath.Join(repo, ".gplay", ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read %s: %v", giPath, err)
	}
	for _, want := range []string{"config.local.json", "edit-*.json"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".gitignore missing %q; got:\n%s", want, data)
		}
	}
}

func TestInit_inHomeDir_isRejected(t *testing.T) {
	home := t.TempDir()
	err := config.Init(home, home, "com.example.app")
	if err == nil {
		t.Fatal("Init in $HOME: expected error, got nil")
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("error %q should mention $HOME path", err.Error())
	}
}

func TestInit_emptyPackage_isRejected(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	err := config.Init(repo, home, "")
	if err == nil {
		t.Fatal("Init with empty package: expected error, got nil")
	}
}

func TestInit_packageWithoutDot_isRejected(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	err := config.Init(repo, home, "notapackage")
	if err == nil {
		t.Fatal("Init with dot-less package: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "notapackage") {
		t.Errorf("error should mention the offending value; got %q", err.Error())
	}
}

func TestInit_isIdempotent_onSecondCall(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(repo, home, "com.example.app"); err != nil {
		t.Fatalf("Init first: %v", err)
	}
	// Append a user line to .gitignore — Init must not blow it away on rerun.
	giPath := filepath.Join(repo, ".gplay", ".gitignore")
	if err := appendLine(giPath, "# my custom rule\n*.swp\n"); err != nil {
		t.Fatal(err)
	}

	if err := config.Init(repo, home, "com.example.app2"); err != nil {
		t.Fatalf("Init second: %v", err)
	}

	data, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read %s: %v", giPath, err)
	}
	if !strings.Contains(string(data), "*.swp") {
		t.Errorf(".gitignore lost user-added line on rerun; got:\n%s", data)
	}

	// config.json should reflect the new pin (Init overwrites it).
	cfgPath := filepath.Join(repo, ".gplay", "config.json")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if !strings.Contains(string(cfg), "com.example.app2") {
		t.Errorf("config.json did not update to new pin; got %s", cfg)
	}
}

// config.json is a committed file (per ADR-0004) shared with teammates,
// so it must use a world-readable mode, not the 0600 reserved for secrets.
func TestInit_configJSON_modeIsWorldReadable(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(repo, home, "com.example.app"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	info, err := os.Stat(filepath.Join(repo, ".gplay", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("config.json mode = %v, want 0644", info.Mode().Perm())
	}
}

// appendLine appends content to path. Helper for the idempotence test.
func appendLine(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	return nil
}

// Make sure the test can detect "the file was never created" cleanly.
func TestInit_inHomeDir_writesNoFiles(t *testing.T) {
	home := t.TempDir()
	_ = config.Init(home, home, "com.example.app") // error expected, we ignore

	if _, err := os.Stat(filepath.Join(home, ".gplay", "config.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Init in $HOME must not create files; stat err = %v", err)
	}
}
