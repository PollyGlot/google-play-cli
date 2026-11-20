package editpin_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/config/configtest"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const pkg = "com.example.app"

type failRenameFS struct{ config.FS }

func (failRenameFS) Rename(_, _ string) error { return errors.New("simulated rename failure") }

func TestFileNameAndPath(t *testing.T) {
	if got := editpin.FileName(pkg); got != "edit-com.example.app.json" {
		t.Errorf("FileName = %q, want edit-com.example.app.json", got)
	}
	if got := editpin.Path("/repo/.gplay", pkg); got != filepath.FromSlash("/repo/.gplay/edit-com.example.app.json") {
		t.Errorf("Path = %q", got)
	}
}

func TestWriteThenLookup(t *testing.T) {
	dir := t.TempDir()
	if err := editpin.Write(config.OSFS{}, dir, pkg, "edit-99"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg)
	if err != nil || !ok {
		t.Fatalf("Lookup ok=%v err=%v, want ok=true nil", ok, err)
	}
	if got.EditID != "edit-99" || got.Package != pkg {
		t.Errorf("pin = %+v, want {edit-99 %s}", got, pkg)
	}
}

func TestWriteCreatesGplayDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".gplay")
	if err := editpin.Write(config.OSFS{}, dir, pkg, "edit-1"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg); err != nil || !ok {
		t.Fatalf("Lookup after Write into fresh dir: ok=%v err=%v", ok, err)
	}
}

func TestWriteFailedSwapKeepsPriorPin(t *testing.T) {
	fsys := configtest.NewMemFS("/", "/home/u")
	dir := "/repo/.gplay"
	path := editpin.Path(dir, pkg)

	if err := editpin.Write(fsys, dir, pkg, "edit-old"); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	if err := editpin.Write(failRenameFS{fsys}, dir, pkg, "edit-new"); err == nil {
		t.Fatal("Write with failing rename: want error, got nil")
	}
	got, ok, err := editpin.Lookup(fsys, dir, pkg)
	if err != nil || !ok {
		t.Fatalf("Lookup after failed swap: ok=%v err=%v, want the prior pin intact", ok, err)
	}
	if got.EditID != "edit-old" {
		t.Errorf("EditID = %q, want edit-old: a failed write must not clobber the prior pin", got.EditID)
	}
	if _, err := fsys.Stat(path + ".tmp"); err == nil {
		t.Error("failed Write left a .tmp fragment behind")
	}
}

func TestWriteLeavesNoTmpBehind(t *testing.T) {
	dir := t.TempDir()
	if err := editpin.Write(config.OSFS{}, dir, pkg, "edit-1"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := (config.OSFS{}).Stat(editpin.Path(dir, pkg) + ".tmp"); err == nil {
		t.Error("Write left a .tmp sibling behind after a successful rename")
	}
}

func TestLookupMissingIsNotAnError(t *testing.T) {
	_, ok, err := editpin.Lookup(config.OSFS{}, t.TempDir(), pkg)
	if err != nil {
		t.Fatalf("Lookup on empty dir: err = %v, want nil", err)
	}
	if ok {
		t.Error("ok = true on a missing pin, want false")
	}
}

func TestLookupUnusablePinIsCorruptError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":         "not json",
		"truncated":        `{"editId": `,
		"missing editId":   `{"package":"com.example.app"}`,
		"package mismatch": `{"editId":"e1","package":"com.other.app"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := (config.OSFS{}).WriteFile(editpin.Path(dir, pkg), []byte(body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg)
			if ok {
				t.Fatal("ok = true on an unusable pin")
			}
			var corrupt *editpin.CorruptError
			if !errors.As(err, &corrupt) {
				t.Fatalf("err = %v (%T), want *editpin.CorruptError", err, err)
			}
			if got := exit.For(err); got != 60 {
				t.Errorf("exit = %d, want 60", got)
			}
			if want := "gplay edits discard --package " + pkg; !strings.Contains(err.Error(), want) {
				t.Errorf("message %q does not name %q", err.Error(), want)
			}
		})
	}
}

func TestClearIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := editpin.Clear(config.OSFS{}, dir, pkg); err != nil {
		t.Fatalf("Clear on missing: %v", err)
	}
	if err := editpin.Write(config.OSFS{}, dir, pkg, "edit-7"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := editpin.Clear(config.OSFS{}, dir, pkg); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, dir, pkg); ok {
		t.Error("pin still present after Clear")
	}
}
