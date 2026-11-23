package imagetree_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func localeWithImages(t *testing.T, localeDir string) {
	t.Helper()
	imgDir := filepath.Join(localeDir, "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"icon.png":         onePNG,
		"promoGraphic.png": onePNG,
	} {
		if err := os.WriteFile(filepath.Join(imgDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func linkLocale(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestScanFollowsLocaleSymlinkedInsideTheTree(t *testing.T) {
	dir := t.TempDir()
	localeWithImages(t, filepath.Join(dir, "fr-CA"))
	linkLocale(t, filepath.Join(dir, "fr-CA"), filepath.Join(dir, "fr-FR"))

	st, err := imagetree.Scan(dir)
	if err != nil {
		t.Fatalf("a locale symlinked inside the tree was refused: %v", err)
	}
	want := []string{filepath.Join(dir, "fr-FR", "images", "icon.png")}
	if got := st["fr-FR"][images.Icon]; !reflect.DeepEqual(got, want) {
		t.Errorf("Scan[fr-FR][icon] = %v, want %v (all of Scan: %v)", got, want, st)
	}
}

func TestRetiredReportsLocaleSymlinkedInsideTheTree(t *testing.T) {
	dir := t.TempDir()
	localeWithImages(t, filepath.Join(dir, "fr-CA"))
	linkLocale(t, filepath.Join(dir, "fr-CA"), filepath.Join(dir, "fr-FR"))

	got, err := imagetree.Retired(dir)
	if err != nil {
		t.Fatalf("Retired: %v", err)
	}
	want := []string{
		filepath.Join(dir, "fr-CA", "images", "promoGraphic.png"),
		filepath.Join(dir, "fr-FR", "images", "promoGraphic.png"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Retired = %v, want %v", got, want)
	}
}

func TestScanFollowsLocaleSymlinkedOutsideUnderTheHatch(t *testing.T) {
	dir := t.TempDir()
	shared := t.TempDir()
	localeWithImages(t, shared)
	linkLocale(t, shared, filepath.Join(dir, "fr-FR"))
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	st, err := imagetree.Scan(dir)
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	if len(st["fr-FR"][images.Icon]) != 1 {
		t.Errorf("the shared locale's icon was not scanned: %v", st)
	}
	if n := strings.Count(notes.String(), "NOTE: "); n != 1 {
		t.Errorf("want exactly one NOTE line for the escaping locale, got %d: %q", n, notes.String())
	}
}

func TestScanAndRetiredRefuseDanglingLocaleSymlink(t *testing.T) {
	dir := t.TempDir()
	linkLocale(t, filepath.Join(dir, "gone"), filepath.Join(dir, "fr-FR"))

	if st, err := imagetree.Scan(dir); err == nil {
		t.Errorf("Scan skipped a dangling locale symlink instead of refusing it: %v", st)
	} else if !strings.Contains(err.Error(), "fr-FR") {
		t.Errorf("the Scan error does not name the locale path: %v", err)
	}
	if got, err := imagetree.Retired(dir); err == nil {
		t.Errorf("Retired skipped a dangling locale symlink instead of refusing it: %v", got)
	} else if !strings.Contains(err.Error(), "fr-FR") {
		t.Errorf("the Retired error does not name the locale path: %v", err)
	}
}
