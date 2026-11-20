package config_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	return repo
}

func gitRepoWithLocalConfig(t *testing.T) string {
	t.Helper()
	repo := gitRepo(t)
	path := filepath.Join(repo, ".gplay", "config.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"account":"client-b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitTracked_untrackedFile_false(t *testing.T) {
	path := gitRepoWithLocalConfig(t)
	if config.GitTracked(context.Background(), path) {
		t.Error("GitTracked = true for a file that was never added")
	}
}

func TestGitTracked_addedFile_true(t *testing.T) {
	path := gitRepoWithLocalConfig(t)
	runGit(t, filepath.Dir(filepath.Dir(path)), "add", "-f", ".gplay/config.local.json")
	if !config.GitTracked(context.Background(), path) {
		t.Error("GitTracked = false for a file in the index")
	}
}

func TestGitTracked_ignoresInheritedGitDir(t *testing.T) {
	path := gitRepoWithLocalConfig(t)
	runGit(t, filepath.Dir(filepath.Dir(path)), "add", "-f", ".gplay/config.local.json")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-a-repo"))
	if !config.GitTracked(context.Background(), path) {
		t.Error("GitTracked followed an inherited GIT_DIR instead of the file's own repository")
	}
}

func TestGitTracked_gplayDirDisguisedAsBareRepo_stillTracked(t *testing.T) {
	path := gitRepoWithLocalConfig(t)
	gplayDir := filepath.Dir(path)
	for _, dir := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(gplayDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gplayDir, dir, ".keep"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gplayDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(gplayDir), "add", "-f", ".gplay")
	if !config.GitTracked(context.Background(), path) {
		t.Error("GitTracked = false: a bare-repo disguise under .gplay/ redirected the check")
	}
}

func TestGitTracked_gplayDirIsCommittedSymlink_stillTracked(t *testing.T) {
	root := gitRepo(t)
	payload := filepath.Join(root, "payload")
	for _, dir := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(payload, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(payload, dir, ".keep"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(payload, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "config.local.json"), []byte(`{"account":"client-b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("payload", filepath.Join(root, ".gplay")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	runGit(t, root, "add", "-f", "payload", ".gplay")
	if !config.GitTracked(context.Background(), filepath.Join(root, ".gplay", "config.local.json")) {
		t.Error("GitTracked = false: a committed .gplay symlink hid the tracked config.local.json")
	}
}

func TestGitTracked_outsideAnyRepository_false(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	path := filepath.Join(t.TempDir(), "config.local.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if config.GitTracked(context.Background(), path) {
		t.Error("GitTracked = true outside any repository")
	}
}

func TestGitTracked_missingDirectory_false(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone", ".gplay", "config.local.json")
	if config.GitTracked(context.Background(), path) {
		t.Error("GitTracked = true for a path whose directory does not exist")
	}
}
