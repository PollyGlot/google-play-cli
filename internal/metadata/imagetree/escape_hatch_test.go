package imagetree_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// sharedAssets builds the layout the hatch exists for, the one the monorepo
// report names: `<dir>/en-US/images` is a symlink at a shared asset directory
// kept outside the metadata tree. It returns the tree root and that directory.
func sharedAssets(t *testing.T) (dir, shared string) {
	t.Helper()
	shared = t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "icon.png"), onePNG, 0o644); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	localeDir := filepath.Join(dir, "en-US")
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(localeDir, "images")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dir, shared
}

// Default: refused, with the variable that brings the layout back named in the
// message, so a monorepo operator reads what to do and not only what broke.
func TestReadRefusesSharedImagesAndNamesTheHatch(t *testing.T) {
	dir, _ := sharedAssets(t)

	if _, err := imagetree.Read(dir); err == nil {
		t.Fatal("Read followed an images/ directory symlinked out of the tree with the hatch unset")
	} else if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", pathguard.EnvAllowExternalSymlinks, err)
	}
}

// Under the opt-in the shared assets are read again, and stderr names what
// left the tree.
func TestReadFollowsSharedImagesUnderTheHatch(t *testing.T) {
	dir, _ := sharedAssets(t)
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	tr, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	seq := tr["en-US"][images.Icon]
	if len(seq) != 1 || !bytes.Equal(seq[0], onePNG) {
		t.Fatalf("the shared icon was not read back: %+v", tr)
	}
	if !strings.Contains(notes.String(), "NOTE: ") {
		t.Errorf("nothing was reported on stderr for a path that left the tree: %q", notes.String())
	}
	if !strings.Contains(notes.String(), "images") {
		t.Errorf("the NOTE does not name the escaping directory: %q", notes.String())
	}
}

// The write side builds its paths from the locale the API returned, so the
// hatch must not reach it: `images pull` into the very same layout is still
// refused, and nothing lands outside the tree.
func TestWriteStaysContainedUnderTheHatch(t *testing.T) {
	dir, shared := sharedAssets(t)
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	tr := imagetree.Tree{"en-US": {images.Icon: {onePNG}}}
	if err := imagetree.Write(dir, tr); err == nil {
		t.Fatal("Write followed a symlink out of the tree because the hatch was set")
	} else if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not explain that the hatch does not cover an API-derived path: %v", err)
	}
	// The pre-existing shared icon must be untouched: a refusal writes nothing.
	got, err := os.ReadFile(filepath.Join(shared, "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, onePNG) {
		t.Error("store content was written outside the tree")
	}
	if notes.Len() != 0 {
		t.Errorf("a refusal must not emit a NOTE, got %q", notes.String())
	}
}
