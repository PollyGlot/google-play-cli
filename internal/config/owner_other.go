//go:build !unix

package config

import "io/fs"

func fileOwner(fs.FileInfo) (int, bool) { return 0, false }
