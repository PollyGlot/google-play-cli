//go:build unix

package config_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// ownersFS is the real filesystem, except that Stat reports the given uid as
// the owner of the listed paths: a test cannot chown to another user, and the
// loader reads ownership only through FS.Stat.
type ownersFS struct {
	config.OSFS
	owners map[string]int
}

func (o ownersFS) Stat(name string) (fs.FileInfo, error) {
	fi, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if uid, ok := o.owners[name]; ok {
		return ownedInfo{fi, uid}, nil
	}
	return fi, nil
}

type ownedInfo struct {
	fs.FileInfo
	uid int
}

func (o ownedInfo) Sys() any { return &syscall.Stat_t{Uid: uint32(o.uid)} }

// TestLoad_foreignProjectDir_isNotAdopted covers a .gplay/ another user
// planted in a directory both users can write to: it must not pick the
// package, the Account or the developer id of commands run below it.
func TestLoad_foreignProjectDir_isNotAdopted(t *testing.T) {
	f := newLoadFixture(t)
	f.writeGlobal(t, `{"accounts":[{"name":"ci","active":true,"developerId":"111"},{"name":"other"}]}`)
	f.writeShared(t, `{"package":"com.example.planted"}`)
	f.writeLocal(t, `{"account":"other","developerId":"999"}`)
	gplayDir := filepath.Dir(f.sharedPath)
	fsys := ownersFS{owners: map[string]int{gplayDir: os.Geteuid() + 1}}

	r, err := config.Load(context.Background(), fsys, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pin != "" {
		t.Errorf("Pin = %q, want empty: the .gplay/ belongs to another user", r.Pin)
	}
	if r.ConfigAccount != "ci" {
		t.Errorf("ConfigAccount = %q, want %q (the global active Account)", r.ConfigAccount, "ci")
	}
	if r.DeveloperID != "111" {
		t.Errorf("DeveloperID = %q, want %q (the global active Account's)", r.DeveloperID, "111")
	}
	if r.ProjectSharedPath != "" || r.ProjectLocalPath != "" {
		t.Errorf("project paths = %q, %q, want empty: no Edit pin may be read from a foreign .gplay/", r.ProjectSharedPath, r.ProjectLocalPath)
	}
	if r.ForeignProjectDir != gplayDir {
		t.Errorf("ForeignProjectDir = %q, want %q", r.ForeignProjectDir, gplayDir)
	}
}

// TestLoad_projectDirOwnedLikeTheWorkingDir_isAdopted covers the documented
// container run: gplay runs as its own uid on a mounted checkout, so the
// .gplay/ is not the current user's but shares the owner of the tree gplay
// works in.
func TestLoad_projectDirOwnedLikeTheWorkingDir_isAdopted(t *testing.T) {
	f := newLoadFixture(t)
	f.writeShared(t, `{"package":"com.example.app"}`)
	host := os.Geteuid() + 1
	fsys := ownersFS{owners: map[string]int{filepath.Dir(f.sharedPath): host, f.cwd: host}}

	r, err := config.Load(context.Background(), fsys, f.loadOpts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Pin != "com.example.app" || r.ForeignProjectDir != "" {
		t.Errorf("Pin = %q, ForeignProjectDir = %q, want com.example.app and empty", r.Pin, r.ForeignProjectDir)
	}
}
