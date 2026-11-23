package pathguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const EnvAllowExternalSymlinks = "GPLAY_ALLOW_EXTERNAL_SYMLINKS"

func externalSymlinksAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvAllowExternalSymlinks))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

var (
	noteMu     sync.Mutex
	noteWriter io.Writer = os.Stderr
	noted                = map[string]bool{}
)

func SetNoteWriter(w io.Writer) func() {
	noteMu.Lock()
	defer noteMu.Unlock()
	prev := noteWriter
	if w == nil {
		w = io.Discard
	}
	noteWriter = w
	noted = map[string]bool{}
	return func() {
		noteMu.Lock()
		defer noteMu.Unlock()
		noteWriter = prev
		noted = map[string]bool{}
	}
}

func noteEscape(key, path, resolved, root string) {
	noteMu.Lock()
	defer noteMu.Unlock()
	if noted[key] {
		return
	}
	noted[key] = true
	_, _ = fmt.Fprintf(noteWriter,
		"NOTE: %q resolves to %q, outside %q; followed because %s is set.\n",
		path, resolved, root, EnvAllowExternalSymlinks)
}

func Root(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", exit.Usagef("cannot resolve directory %q: %v", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func Contain(root, path string) (string, error) {
	abs, resolved, err := resolveCandidate(root, path)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		hatch := ""
		if externalSymlinksAllowed() {
			hatch = fmt.Sprintf(
				" %s is set, but it does not cover this path: it is derived from data the API returned (a locale, a package name), not from your own directory tree.",
				EnvAllowExternalSymlinks)
		}
		return "", exit.Containmentf(
			"refusing to use %q: it resolves to %q, outside %q. A symlink or a %q component in a repo-controlled path cannot be followed out of its directory.%s",
			path, resolved, root, "..", hatch)
	}
	return abs, nil
}

func ContainUserPath(root, path string) (string, error) {
	abs, resolved, err := resolveCandidate(root, path)
	if err != nil {
		return "", err
	}
	if within(root, resolved) {
		return abs, nil
	}
	if !externalSymlinksAllowed() {
		return "", exit.Containmentf(
			"refusing to use %q: it resolves to %q, outside %q. A symlink or a %q component in a repo-controlled path cannot be followed out of its directory. If this tree legitimately shares files with another one (a monorepo layout), set %s=1 to follow links out of it.",
			path, resolved, root, "..", EnvAllowExternalSymlinks)
	}
	if !spelledUnder(root, abs) {
		return "", exit.Containmentf(
			"refusing to use %q: it resolves to %q, outside %q. %s follows a symlink pointing out of the tree; it does not allow a %q component climbing above the root.",
			path, resolved, root, EnvAllowExternalSymlinks, "..")
	}
	noteEscape(abs, path, resolved, root)
	return abs, nil
}

func resolveCandidate(root, path string) (abs, resolved string, err error) {
	if root == "" {
		return "", "", exit.Usagef("internal: path containment called with an empty root for %q", path)
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", "", exit.Usagef("cannot resolve path %q: %v", path, err)
	}
	resolved, err = resolveExisting(abs)
	if err != nil {
		return "", "", err
	}
	return abs, resolved, nil
}

func spelledUnder(root, abs string) bool {
	cur := filepath.Clean(abs)
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil && resolved == root {
			return true
		}
	}
}

func ContainWrite(root, path string) (string, error) {
	abs, err := Contain(root, path)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil
		}
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", exit.Usagef(
			"refusing to write %q: it is a symlink, and writing would go through it to %q. Replace the link with a regular file.",
			path, linkTarget(abs))
	}
	return abs, nil
}

func linkTarget(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return "its target"
	}
	return target
}

func resolveExisting(abs string) (string, error) {
	rest := ""
	cur := filepath.Clean(abs)
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func Segment(what, name string) error {
	switch {
	case name == "":
		return exit.Usagef("%s is empty: it is used as a file name and cannot be blank", what)
	case name == "." || name == "..":
		return exit.Usagef("refusing to use %s %q: it is a directory reference, not a name", what, name)
	case filepath.IsAbs(name):
		return exit.Usagef("refusing to use %s %q: it is an absolute path, not a name", what, name)
	case strings.ContainsRune(name, '/'), strings.ContainsRune(name, os.PathSeparator):
		return exit.Usagef("refusing to use %s %q: it contains a path separator, and is used as a file name", what, name)
	case strings.ContainsRune(name, 0):
		return exit.Usagef("refusing to use %s %q: it contains a NUL byte", what, name)
	}
	if filepath.Clean(name) != name {
		return exit.Usagef("refusing to use %s %q: it is not a plain file name", what, name)
	}
	return nil
}

func JoinSegment(what, dir, name string) (string, error) {
	if err := Segment(what, name); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
