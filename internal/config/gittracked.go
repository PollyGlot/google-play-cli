package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/gitenv"
)

const gitTrackedTimeout = 2 * time.Second

func GitTracked(ctx context.Context, path string) bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	path = filepath.Join(dir, filepath.Base(path))
	top, ok := worktreeRoot(dir)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(top, path)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, gitTrackedTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "-c", "core.fsmonitor=false", "ls-files", "--error-unmatch", "--", rel) // #nosec G204 -- git from PATH, fixed arguments, the file name passed after "--"
	cmd.Dir = top
	cmd.Env = append(gitenv.Safe(os.Environ()), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	return cmd.Run() == nil
}

func worktreeRoot(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
