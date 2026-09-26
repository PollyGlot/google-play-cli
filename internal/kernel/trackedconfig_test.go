package kernel_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// cwdFS is the real filesystem seen from a chosen working directory and home,
// so the config walk-up starts inside a throwaway repository without the test
// changing the process's cwd.
type cwdFS struct {
	config.OSFS
	cwd, home string
}

func (f cwdFS) Getwd() (string, error)       { return f.cwd, nil }
func (f cwdFS) UserHomeDir() (string, error) { return f.home, nil }

// repoWithLocalConfig builds a git repository holding .gplay/config.local.json
// and returns its root. track decides whether the file is added to the index.
func repoWithLocalConfig(t *testing.T, track bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo
		// Drop inherited GIT_* so a run from a git hook still targets repo.
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GIT_") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	local := filepath.Join(repo, ".gplay", "config.local.json")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`{"developerId":"123"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if track {
		git("add", "-f", ".gplay/config.local.json")
	}
	return repo
}

func runInRepo(t *testing.T, repo string) string {
	t.Helper()
	boot := newBoot(t)
	var stderr bytes.Buffer
	boot.Stderr = &stderr
	boot.FS = cwdFS{cwd: repo, home: t.TempDir()}
	if err := kernel.Run(boot, kernel.Inputs{}, func(*kernel.RunContext) (output.Renderable, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return stderr.String()
}

// TestRun_trackedLocalConfig_warnsOnStderr asserts a committed
// .gplay/config.local.json draws one warning naming the file and the fix: it
// selects the Account every clone runs with, and only a .gitignore kept it
// out of the repository (#603).
func TestRun_trackedLocalConfig_warnsOnStderr(t *testing.T) {
	repo := repoWithLocalConfig(t, true)
	got := runInRepo(t, repo)
	if strings.Count(got, "warning: ") != 1 {
		t.Fatalf("want exactly one warning on stderr, got %q", got)
	}
	for _, want := range []string{"config.local.json is tracked by git", "git rm --cached"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q is missing %q", got, want)
		}
	}
}

func TestRun_untrackedLocalConfig_isSilent(t *testing.T) {
	repo := repoWithLocalConfig(t, false)
	if got := runInRepo(t, repo); got != "" {
		t.Errorf("an untracked config.local.json produced stderr output: %q", got)
	}
}
