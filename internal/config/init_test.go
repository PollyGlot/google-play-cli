package config_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestInit_writesConfigJSON_withPackagePin(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(context.Background(), config.OSFS{}, repo, home, "com.example.app"); err != nil {
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

	if err := config.Init(context.Background(), config.OSFS{}, repo, home, "com.example.app"); err != nil {
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
	err := config.Init(context.Background(), config.OSFS{}, home, home, "com.example.app")
	if err == nil {
		t.Fatal("Init in $HOME: expected error, got nil")
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("error %q should mention $HOME path", err.Error())
	}
	if code := exit.For(err); code != 2 {
		t.Errorf("exit.For = %d, want 2 (CLI misuse)", code)
	}
}

func TestInit_emptyPackage_isRejected(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	err := config.Init(context.Background(), config.OSFS{}, repo, home, "")
	if err == nil {
		t.Fatal("Init with empty package: expected error, got nil")
	}
}

func TestInit_packageWithoutDot_isRejected(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	err := config.Init(context.Background(), config.OSFS{}, repo, home, "notapackage")
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

	if err := config.Init(context.Background(), config.OSFS{}, repo, home, "com.example.app"); err != nil {
		t.Fatalf("Init first: %v", err)
	}
	giPath := filepath.Join(repo, ".gplay", ".gitignore")
	if err := appendLine(giPath, "# my custom rule\n*.swp\n"); err != nil {
		t.Fatal(err)
	}

	if err := config.Init(context.Background(), config.OSFS{}, repo, home, "com.example.app2"); err != nil {
		t.Fatalf("Init second: %v", err)
	}

	data, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read %s: %v", giPath, err)
	}
	if !strings.Contains(string(data), "*.swp") {
		t.Errorf(".gitignore lost user-added line on rerun; got:\n%s", data)
	}

	cfgPath := filepath.Join(repo, ".gplay", "config.json")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if !strings.Contains(string(cfg), "com.example.app2") {
		t.Errorf("config.json did not update to new pin; got %s", cfg)
	}
}

func TestInit_configJSON_modeIsWorldReadable(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()

	if err := config.Init(context.Background(), config.OSFS{}, repo, home, "com.example.app"); err != nil {
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

func TestInit_inHomeDir_writesNoFiles(t *testing.T) {
	home := t.TempDir()
	_ = config.Init(context.Background(), config.OSFS{}, home, home, "com.example.app")

	if _, err := os.Stat(filepath.Join(home, ".gplay", "config.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Init in $HOME must not create files; stat err = %v", err)
	}
}

func TestInit_refusesToWriteThroughCommittedSymlink(t *testing.T) {
	for _, file := range []string{"config.json", ".gitignore"} {
		for _, tc := range []struct {
			where, wantMsg string
		}{
			{"outside", "outside"},
			{"inside", "it is a symlink"},
		} {
			t.Run(file+"/target-"+tc.where, func(t *testing.T) {
				repo := gitRepo(t)
				victimDir := t.TempDir()
				if tc.where == "inside" {
					victimDir = repo
				}
				victim := filepath.Join(victimDir, "victim.txt")
				const victimContent = "VICTIM-ORIGINAL"
				if err := os.WriteFile(victim, []byte(victimContent), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(repo, ".gplay"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, filepath.Join(repo, ".gplay", file)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				runGit(t, repo, "add", "-A")

				err := config.Init(context.Background(), config.OSFS{}, repo, t.TempDir(), "com.example.app")
				var ue *exit.UsageError
				if !errors.As(err, &ue) {
					t.Errorf("want a usage error (exit 2), got %T: %v", err, err)
				} else if !strings.Contains(err.Error(), tc.wantMsg) || !strings.Contains(err.Error(), file) {
					t.Errorf("want a refusal naming %s (%q), got %v", file, tc.wantMsg, err)
				}
				assertUnchanged(t, victim, victimContent)
			})
		}
	}
}

func TestInit_refusesSymlinkedGplayDirOutsideRepo(t *testing.T) {
	repo := gitRepo(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(repo, ".gplay")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := config.Init(context.Background(), config.OSFS{}, repo, t.TempDir(), "com.example.app")
	var ue *exit.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("want a usage error (exit 2), got %T: %v", err, err)
	}
	entries, readErr := os.ReadDir(elsewhere)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("Init wrote %d file(s) through the symlinked .gplay/", len(entries))
	}
}

func assertUnchanged(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the victim file is gone: %v", err)
	}
	if string(got) != want {
		t.Errorf("Init wrote through the symlink:\n%s", got)
	}
}
