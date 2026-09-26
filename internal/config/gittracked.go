package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTrackedTimeout bounds the one git call GitTracked makes. It runs on every
// command started inside a repo holding .gplay/config.local.json, so a git
// stuck on a network filesystem or a lock must cost the command a short delay,
// never a hang.
const gitTrackedTimeout = 2 * time.Second

// gitRedirectVars move git's idea of which repository or index it reads.
// Inherited from a hook or an alias, GIT_DIR would make the check answer for
// the caller's repository instead of the one around the file. The config
// injection pair goes too: it is `-c` by another name and could re-enable the
// fsmonitor hook GitTracked disables. Same list, same reason as install-skills'
// gitSafeEnv.
var gitRedirectVars = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_COMMON_DIR":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE":                    true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_CONFIG_PARAMETERS":            true,
}

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
// off; ls-files runs no hooks, filters or pager.
func GitTracked(ctx context.Context, path string) bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, gitTrackedTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "-c", "core.fsmonitor=false", "ls-files", "--error-unmatch", "--", filepath.Base(path)) // #nosec G204 -- git from PATH, fixed arguments, the file name passed after "--"
	cmd.Dir = filepath.Dir(path)
	cmd.Env = append(gitCleanEnv(os.Environ()), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	// Exit 0 means the index lists the file; 1 means untracked, and 128 means
	// no repository (or no directory) at all. Only 0 is "tracked".
	return cmd.Run() == nil
}

func gitCleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if gitRedirectVars[name] || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
