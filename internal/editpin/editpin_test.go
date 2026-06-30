package editpin_test

import (
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
)

const pkg = "com.example.app"

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
