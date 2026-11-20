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

func TestReadRefusesSharedImagesAndNamesTheHatch(t *testing.T) {
	dir, _ := sharedAssets(t)

	if _, err := imagetree.Read(dir); err == nil {
		t.Fatal("Read followed an images/ directory symlinked out of the tree with the hatch unset")
	} else if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", pathguard.EnvAllowExternalSymlinks, err)
	}
}

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
