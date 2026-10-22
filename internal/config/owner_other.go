//go:build !unix

package config

import "io/fs"

// fileOwner reports no owner off Unix: Windows ACLs have no single uid to
// compare, so the ownership check does not apply there.
func fileOwner(fs.FileInfo) (int, bool) { return 0, false }
