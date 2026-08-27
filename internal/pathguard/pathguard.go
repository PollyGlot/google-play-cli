// Package pathguard keeps repo-controlled file inputs inside their intended
// root.
//
// gplay reads and writes directory trees a repo owns: a release-notes
// directory, a metadata or images directory, the `.gplay/` state dir. Those
// trees are walked by name, and a name can lie. A `fr.txt` symlinked to
// `~/.ssh/id_rsa` gets uploaded to a store listing; a locale directory called
// `..` writes outside the tree; a `--package ../../x` turns an edit pin into a
// write anywhere. None of it needs a compromised machine, only a repo whose
// contents the operator did not audit, run with the operator's credentials
// (PRD #459 / slice #461).
//
// The defence is containment: resolve the root and the candidate through
// EvalSymlinks, then require the resolved candidate to be at or under the
// resolved root. Resolving BOTH sides is what keeps a legitimately symlinked
// checkout working: on macOS /tmp is itself a symlink to /private/tmp, and a
// lexical-only check would reject every path under it.
//
// The guards are always on: there is no flag to disable them.
package pathguard

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Root resolves dir to its canonical, symlink-free absolute form: the value to
// pass as the root of every subsequent Contain call. Resolve the root ONCE per
// operation and reuse it; resolving it per file would both cost a syscall
// storm and, worse, let the root itself be swapped mid-walk.
//
// dir must exist. A caller that has already established the directory exists
// (every one of gplay's, since they ReadDir it first) can treat a failure here
// as the same not-found error it would have got anyway.
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

// Contain checks that path stays inside root (which must already be a Root
// result) and returns the resolved path to use for the actual I/O.
//
// It handles the not-yet-existing case, which the write paths need: when path
// does not exist, the nearest existing ANCESTOR is resolved and the remaining
// components are re-appended lexically. That is sound because a component that
// does not exist cannot be a symlink; the escape has to come from an ancestor
// that does exist, and that ancestor is resolved.
//
// An escape is a usage-class failure (exit 2) naming the offending path: it is
// a bad input, not a transient error, so retrying is pointless and the message
// must say which path to fix.
func Contain(root, path string) (string, error) {
	if root == "" {
		return "", exit.Usagef("internal: path containment called with an empty root for %q", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", exit.Usagef("cannot resolve path %q: %v", path, err)
	}

	resolved, err := resolveExisting(abs)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		return "", exit.Usagef(
			"refusing to use %q: it resolves to %q, outside %q. A symlink or a %q component in a repo-controlled path cannot be followed out of its directory.",
			path, resolved, root, "..")
	}
	// Return the ORIGINAL absolute path, not the resolved one: the caller opens
	// what it named, and we have just proved that name lands inside the root.
	// Handing back the resolved path would silently rewrite the file names that
	// appear in the caller's own error messages.
	return abs, nil
}

// resolveExisting is EvalSymlinks extended to paths whose leaf does not exist
// yet, by resolving the deepest ancestor that does and re-appending the rest.
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
			// Walked to the filesystem root without finding anything that
			// exists: nothing can be resolved, fall back to the lexical form.
			// filepath.Clean has already collapsed any "..", so this is safe to
			// compare, it just cannot see through a symlink that is not there.
			return filepath.Clean(abs), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether path is root itself or lies under it. It uses Rel
// rather than a string prefix, so a sibling directory whose name merely starts
// with the root's ("/repo/.gplay-evil" against root "/repo/.gplay") is
// correctly rejected.
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

// Segment validates that name is a single, ordinary path component safe to
// join into a path: no separator, no "." or "..", not empty, not absolute.
//
// It is the cheap guard for the case where a name arrives from outside the
// filesystem entirely, so there is no directory to contain it against: a
// package name flowing into `.gplay/edit-<package>.json`, or an API-supplied
// locale or product id becoming a directory. `what` names the input in the
// error, so the message says what to fix rather than just what was rejected.
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
	// Belt and braces: anything Clean rewrites was not a plain component.
	if filepath.Clean(name) != name {
		return exit.Usagef("refusing to use %s %q: it is not a plain file name", what, name)
	}
	return nil
}

// JoinSegment validates name with Segment and joins it under dir. It is the
// one-call form for the common "build <dir>/<untrusted-name>" case.
func JoinSegment(what, dir, name string) (string, error) {
	if err := Segment(what, name); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
