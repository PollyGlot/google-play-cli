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

// gitRepoWithLocalConfig creates a throwaway git repository holding
// .gplay/config.local.json, untracked, and returns the file's path.
func gitRepoWithLocalConfig(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	path := filepath.Join(repo, ".gplay", "config.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"account":"client-b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runGit runs git in dir with every inherited GIT_* variable dropped, so a
// test run from a git hook (which exports GIT_DIR) still builds its fixture in
// dir and not in the repository the hook belongs to.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
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

// TestGitTracked_ignoresInheritedGitDir asserts an inherited GIT_DIR (set by
// git for every hook it runs) does not redirect the check to another
// repository: the answer is about the repository around the file.
func TestGitTracked_ignoresInheritedGitDir(t *testing.T) {
	path := gitRepoWithLocalConfig(t)
	runGit(t, filepath.Dir(filepath.Dir(path)), "add", "-f", ".gplay/config.local.json")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-a-repo"))
	if !config.GitTracked(context.Background(), path) {
		t.Error("GitTracked followed an inherited GIT_DIR instead of the file's own repository")
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
