package editpin_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
)

const pkg = "com.example.app"

// failRenameFS wraps an FS but makes the tmp→pin rename fail, simulating a swap
// that dies partway (EXDEV, ENOSPC, power loss). Every other operation delegates
// to the embedded FS, so the tmp file still gets written and best-effort-removed.
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
	// Write into a .gplay/ that does not exist yet — begin must not require
	// the directory to be pre-created (beyond the project root).
	dir := filepath.Join(t.TempDir(), ".gplay")
	if err := editpin.Write(config.OSFS{}, dir, pkg, "edit-1"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg); err != nil || !ok {
		t.Fatalf("Lookup after Write into fresh dir: ok=%v err=%v", ok, err)
	}
}

// TestWriteFailedSwapKeepsPriorPin is the crash-atomicity guard: a Write whose
// final swap fails must leave the *prior* pin intact, never a truncated or
// half-written one. Because a corrupt pin is fatal to Lookup (and would wedge
// every later write command plus block `gplay edits begin` recovery), Write
// stages to `<pin>.tmp` and renames — so a failed write is a no-op on the live
// pin. A non-atomic WriteFile-in-place would clobber it. MemFS mirrors the
// FS-level contract (as config's atomic-Save test does).
func TestWriteFailedSwapKeepsPriorPin(t *testing.T) {
	fsys := config.NewMemFS("/", "/home/u")
	dir := "/repo/.gplay"
	path := editpin.Path(dir, pkg)

	// A valid pin already on disk (a prior `gplay edits begin`).
	if err := editpin.Write(fsys, dir, pkg, "edit-old"); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	// The swap onto the live pin fails partway; the prior pin must survive.
	if err := editpin.Write(failRenameFS{fsys}, dir, pkg, "edit-new"); err == nil {
		t.Fatal("Write with failing rename: want error, got nil")
	}
	got, ok, err := editpin.Lookup(fsys, dir, pkg)
	if err != nil || !ok {
		t.Fatalf("Lookup after failed swap: ok=%v err=%v, want the prior pin intact", ok, err)
	}
	if got.EditID != "edit-old" {
		t.Errorf("EditID = %q, want edit-old — a failed write must not clobber the prior pin", got.EditID)
	}
	// No `.tmp` fragment left masquerading as pin state.
	if _, err := fsys.Stat(path + ".tmp"); err == nil {
		t.Error("failed Write left a .tmp fragment behind")
	}
}

// TestWriteLeavesNoTmpBehind asserts a successful fresh write (no prior pin)
// lands the pin and cleans up after itself — the rename consumes the `.tmp`
// sibling, leaving nothing for a later Lookup to trip over.
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

func TestLookupCorruptIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := (config.OSFS{}).WriteFile(editpin.Path(dir, pkg), []byte("not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg); err == nil || ok {
		t.Errorf("corrupt pin: ok=%v err=%v, want ok=false non-nil err", ok, err)
	}
}

func TestLookupMissingEditIDIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := (config.OSFS{}).WriteFile(editpin.Path(dir, pkg), []byte(`{"package":"com.example.app"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg); err == nil || ok {
		t.Errorf("pin without editId: ok=%v err=%v, want ok=false non-nil err", ok, err)
	}
}

func TestLookupPackageMismatchIsAnError(t *testing.T) {
	dir := t.TempDir()
	// File is edit-com.example.app.json but its embedded package disagrees — a
	// copied/renamed pin. Lookup must reject it as corruption.
	if err := (config.OSFS{}).WriteFile(editpin.Path(dir, pkg), []byte(`{"editId":"e1","package":"com.other.app"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok, err := editpin.Lookup(config.OSFS{}, dir, pkg); err == nil || ok {
		t.Errorf("package mismatch: ok=%v err=%v, want ok=false non-nil err", ok, err)
	}
}

func TestClearIdempotent(t *testing.T) {
	dir := t.TempDir()
	// Clearing a non-existent pin is a no-op (no error).
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
