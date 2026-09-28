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
func GitTracked(ctx context.Context, path string) bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, gitTrackedTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "-c", "core.fsmonitor=false", "ls-files", "--error-unmatch", "--", filepath.Base(path)) // #nosec G204 -- git from PATH, fixed arguments, the file name passed after "--"
	cmd.Dir = filepath.Dir(path)
	cmd.Env = append(gitenv.Safe(os.Environ()), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	// Exit 0 means the index lists the file; 1 means untracked, and 128 means
	// no repository (or no directory) at all. Only 0 is "tracked".
	return cmd.Run() == nil
}
