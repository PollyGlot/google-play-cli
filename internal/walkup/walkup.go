package walkup

import (
	"os"
	"path/filepath"
	"strings"
)

func FindFile(start, filename string) (string, error) {
	return FindFileExcluding(start, filename, "")
}

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

func isAtOrAbove(dir, exclude string) bool {
	return dir == exclude || strings.HasPrefix(exclude, dir+string(filepath.Separator))
}
