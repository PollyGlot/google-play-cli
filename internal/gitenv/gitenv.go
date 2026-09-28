// Package gitenv strips the inherited environment variables that would point a
// git child process at another repository than the directory gplay hands it.
// It is shared by every place gplay runs git: `install-skills` (clone into a
// disposable directory) and the config loader's tracked-file check (#603).
package gitenv

import "strings"

// locationVars are the inherited variables that move git's idea of *which*
// repository it is operating on. Setting the command's Dir is not enough: with
// GIT_DIR (or GIT_WORK_TREE) in the environment, `git init`, `remote add
// origin` and `checkout --detach` all land on the caller's repository instead
// of the intended directory, silently rewriting their HEAD, refs and remotes,
// and `ls-files` answers for the wrong index. That environment is ordinary,
// not exotic: git sets GIT_DIR for every hook it runs, so gplay run from a
// pre-commit hook or a git alias would hit it.
//
// The environment's *config injection* channel (GIT_CONFIG_COUNT and its
// numbered key/value pairs, GIT_CONFIG_PARAMETERS) goes for the same reason at
// one remove: it is `-c` by another name, so an inherited pair could re-enable
// what a caller's own `-c` disables (core.fsmonitor, hooks). The config
// *files* are left alone on purpose, so a corporate proxy or CA bundle keeps
// working.
var locationVars = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_COMMON_DIR":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_NAMESPACE":                    true,
	"GIT_CEILING_DIRECTORIES":          true,
	"GIT_TEMPLATE_DIR":                 true,
	"GIT_CONFIG":                       true,
	"GIT_CONFIG_COUNT":                 true,
	"GIT_CONFIG_PARAMETERS":            true,
}

// configVarPrefixes cover the numbered GIT_CONFIG_KEY_<n> /
// GIT_CONFIG_VALUE_<n> pairs, which are the environment form of `-c`.
var configVarPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

// Safe returns env without the variables that would redirect git away from
// the directory it is run in. Everything else is passed through: PATH, proxy
// and TLS settings, and the user's own git configuration all still apply.
func Safe(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if locationVars[name] || hasAnyPrefix(name, configVarPrefixes) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
