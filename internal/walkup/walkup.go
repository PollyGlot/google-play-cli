// Package walkup finds a file by walking up from a starting directory
// through its ancestors, mirroring how `git` discovers the repository root
// from any subdirectory.
//
// This is the discovery primitive behind gplay's cascading config: from any
// cwd inside a repo, FindFile locates the nearest `.gplay/config.json`
// without the caller having to know where the repo root is.
package walkup

import (
	"os"
	"path/filepath"
	"strings"
)

// FindFile walks up from start (inclusive) through every ancestor directory
// looking for filename. Returns the directory containing filename, or empty
// string + os.ErrNotExist if not found anywhere up to the filesystem root.
func FindFile(start, filename string) (string, error) {
	return FindFileExcluding(start, filename, "")
}

// FindFileExcluding is FindFile with a barrier: dirToExclude (and every
// directory at or above it) is treated as if filename did not exist there.
// Used by gplay's config loader to refuse picking up a stray
// ~/.gplay/config.json as if it were a project pin, which would let any
// repo on the machine silently inherit a foreign package.
//
// If dirToExclude is empty, behaves like an unrestricted walk.
func FindFileExcluding(start, filename, dirToExclude string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	exclude := ""
	if dirToExclude != "" {
		exclude, err = filepath.Abs(dirToExclude)
		if err != nil {
			return "", err
		}
	}
	for {
		if exclude == "" || !isAtOrAbove(dir, exclude) {
			candidate := filepath.Join(dir, filename)
			if _, err := os.Stat(candidate); err == nil {
				return dir, nil
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// isAtOrAbove reports whether dir is exclude or an ancestor of exclude.
// Both arguments must be absolute, cleaned paths.
func isAtOrAbove(dir, exclude string) bool {
	rel, err := filepath.Rel(dir, exclude)
	if err != nil {
		return false
	}
	// Rel returns a path of one or more components separated by Separator.
	// "exclude is at or below dir" iff the first component is not "..".
	first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	return first != ".."
}
