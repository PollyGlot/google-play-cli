package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/gitenv"
)

// gitTrackedTimeout bounds the one git call GitTracked makes. It runs on every
// command started inside a repo holding .gplay/config.local.json, so a git
// stuck on a network filesystem or a lock must cost the command a short delay,
// never a hang.
const gitTrackedTimeout = 2 * time.Second

// GitTracked reports whether path is tracked by the git repository that
// contains it. It answers false whenever it cannot tell: git not installed, the
// file outside any repository, a timeout. The caller only uses it to warn, and
// a missing warning is the state every machine without git is in anyway.
//
// It exists for .gplay/config.local.json (ADR-0004): that file picks the
// Account a command runs with, and only the .gitignore gplay writes keeps it
// out of version control. A repo that commits it anyway can point whoever
// clones it at another of their registered Accounts, silently (#603).
//
// git runs inside a directory the repo controls, so the one config key that
// makes a read-only git command execute a program, core.fsmonitor, is forced
// off; ls-files runs no hooks, filters or pager. gitenv.Safe drops an
// inherited GIT_DIR (set for every git hook), which would make the check
// answer for the caller's repository, and the `-c` injection variables that
// could turn fsmonitor back on.
//
// git does not run from the file's own directory: committing HEAD, objects/ and
// refs/ under .gplay/ makes that directory pass for a bare repository, which
// git's discovery would find first and answer "untracked" for (#791). It runs
// from the nearest ancestor holding a .git entry instead, a name git refuses to
// check out, so no clone can plant one; discovery there stops at that .git.
func GitTracked(ctx context.Context, path string) bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	top, ok := worktreeRoot(filepath.Dir(path))
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
	// Exit 0 means the index lists the file; 1 means untracked, and 128 means
	// no usable repository. Only 0 is "tracked".
	return cmd.Run() == nil
}

// worktreeRoot returns the nearest directory at or above dir that holds a .git
// entry (a directory, or the file a linked worktree or submodule uses).
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
