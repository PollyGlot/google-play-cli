// Package walkup walks up from a starting directory looking for a file,
// mirroring git's `.git/` discovery.
package walkup

import (
	"os"
	"path/filepath"
	"strings"
)

// FindFile walks up from start (inclusive) looking for filename. Returns
// the containing directory, or os.ErrNotExist if not found up to the root.
func FindFile(start, filename string) (string, error) {
	return FindFileExcluding(start, filename, "")
}

// FindFileExcluding is FindFile with a barrier: filename is ignored at or
// above dirToExclude. Pass "" for no barrier.
func FindFileExcluding(start, filename, dirToExclude string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	var exclude string
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
	return dir == exclude || strings.HasPrefix(exclude, dir+string(filepath.Separator))
}
