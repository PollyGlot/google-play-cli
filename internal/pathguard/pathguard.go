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
// Containment is closed by default and has no global off switch. It does have
// one narrow, opt-in escape hatch, GPLAY_ALLOW_EXTERNAL_SYMLINKS, for the
// monorepo layouts that legitimately share files between trees
// (`metadata/en-US/images` symlinked at `shared/assets/en-US`, a `title.txt`
// pointing at a shared translation). The hatch covers ContainUserPath only:
// paths gplay found by walking the operator's OWN directory tree. Paths built
// from data the API returned (a locale, a package name) go through Contain and
// stay contained whatever the environment says, because that is the input the
// operator never got to audit.
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

// EnvAllowExternalSymlinks is the opt-in escape hatch described in the package
// doc. It is honoured when the env var holds a truthy value (1/true/yes/on,
// case-insensitive), matching GPLAY_READONLY's spelling; anything else (unset,
// "0", "false", ...) leaves containment closed, which is the default and the
// only state a fresh environment can be in.
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

// SetNoteWriter points the NOTE stream at w and returns a function restoring
// the previous writer. cmd/gplay hands it the process's redacting stderr;
// tests use the restore to capture what was emitted.
//
// A package-level sink rather than a parameter: the codecs that call
// ContainUserPath (metadata/tree, metadata/imagetree, releases/notes) take a
// directory and nothing else, and threading a writer through all of them to
// report a condition that is off by default would cost every signature. Notes
// go to stderr, never stdout, which mirrors the API verbatim (ADR-0003).
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

// noteEscape reports on stderr that a path left the tree under the hatch. The
// hatch is opt-in, not silent: the operator asked for the layout, they still
// get a log line naming every file that actually left.
//
// Deduped per named path, because one symlinked directory is contained once
// per file underneath it and would otherwise print the same fact N times.
func noteEscape(key, path, resolved, root string) {
	noteMu.Lock()
	defer noteMu.Unlock()
	if noted[key] {
		return
	}
	noted[key] = true
	// A failed write to stderr is not worth failing the command over: the file
	// is contained, the operator asked for it, and the log line is advisory.
	_, _ = fmt.Fprintf(noteWriter,
		"NOTE: %q resolves to %q, outside %q; followed because %s is set.\n",
		path, resolved, root, EnvAllowExternalSymlinks)
}

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
	abs, resolved, err := resolveCandidate(root, path)
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		// The hatch is named here ONLY to close it explicitly. An operator who
		// set it and still gets refused would otherwise conclude it is broken;
		// the truth is that this path was not built from their tree, so the
		// hatch was never going to cover it.
		hatch := ""
		if externalSymlinksAllowed() {
			hatch = fmt.Sprintf(
				" %s is set, but it does not cover this path: it is derived from data the API returned (a locale, a package name), not from your own directory tree.",
				EnvAllowExternalSymlinks)
		}
		return "", exit.Usagef(
			"refusing to use %q: it resolves to %q, outside %q. A symlink or a %q component in a repo-controlled path cannot be followed out of its directory.%s",
			path, resolved, root, "..", hatch)
	}
	// Return the ORIGINAL absolute path, not the resolved one: the caller opens
	// what it named, and we have just proved that name lands inside the root.
	// Handing back the resolved path would silently rewrite the file names that
	// appear in the caller's own error messages.
	return abs, nil
}

// ContainUserPath is Contain for a path gplay reached by walking the
// operator's OWN directory tree: a locale directory found by ReadDir, a
// `title.txt` next to it, a release-note file. Those names are the operator's
// to arrange, so they are the ones the escape hatch covers; everything derived
// from API data keeps using Contain and stays contained regardless.
//
// Default (hatch unset), it is Contain, with a refusal that names the hatch so
// a monorepo operator reads what to do rather than just what broke.
//
// With GPLAY_ALLOW_EXTERNAL_SYMLINKS truthy, a path that leaves the tree
// THROUGH A SYMLINK is followed, and a NOTE on stderr says so. A path that
// leaves it by climbing (a ".." above the root) is still refused: the hatch
// opens a door out of the tree, it does not turn containment off, and the
// distinction is what keeps the refusal meaningful for anything the operator
// did not spell out as a link.
func ContainUserPath(root, path string) (string, error) {
	abs, resolved, err := resolveCandidate(root, path)
	if err != nil {
		return "", err
	}
	if within(root, resolved) {
		return abs, nil
	}
	if !externalSymlinksAllowed() {
		return "", exit.Usagef(
			"refusing to use %q: it resolves to %q, outside %q. A symlink or a %q component in a repo-controlled path cannot be followed out of its directory. If this tree legitimately shares files with another one (a monorepo layout), set %s=1 to follow links out of it.",
			path, resolved, root, "..", EnvAllowExternalSymlinks)
	}
	if !spelledUnder(root, abs) {
		return "", exit.Usagef(
			"refusing to use %q: it resolves to %q, outside %q. %s follows a symlink pointing out of the tree; it does not allow a %q component climbing above the root.",
			path, resolved, root, EnvAllowExternalSymlinks, "..")
	}
	noteEscape(abs, path, resolved, root)
	return abs, nil
}

// resolveCandidate does the work Contain and ContainUserPath share: validate
// the root, absolutize the candidate, and resolve it (see Contain for why a
// not-yet-existing leaf resolves through its nearest existing ancestor).
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

// spelledUnder reports whether abs is *spelled* inside root: whether some
// ancestor directory of abs resolves to root. Once the resolved path is known
// to be outside, this is what separates the two ways out of a tree: a symlink
// below the root (every component still written inside it, one of them a link)
// from a ".." that climbed above the root before any link was involved.
//
// Ancestors are compared by their RESOLVED form, not by string, so it still
// holds when the root itself is reached through a symlink: macOS's
// /var -> /private/var, or a checkout behind a link. Walking up costs one
// EvalSymlinks per level, which is fine because this only runs on the escape
// path, under an opt-in that is off by default.
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

// ContainWrite is Contain for a path that is about to be WRITTEN, and adds the
// one check a read does not need: the leaf must not itself be a symlink.
//
// Contain alone is not enough on a write. It resolves the path and proves the
// TARGET is inside root, which is exactly right for a read, but os.WriteFile
// follows the link: a `1.png` or `title.txt` pre-placed in the repo as a
// symlink turns `pull` into "overwrite the operator's file with store content".
// A link pointing outside root is already refused by Contain; a link pointing
// back inside root would pass it, and still write to a file the caller did not
// name. Refusing to write through any symlink covers both, and it is the
// symmetric counterpart of the containment applied on the read side.
//
// A path that does not exist yet is the normal case and passes.
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

// linkTarget reads a symlink for the error message only; an unreadable link
// still gets a usable message rather than swallowing the refusal.
func linkTarget(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return "its target"
	}
	return target
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
