package tree

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

func writeTitle(t *testing.T, localeDir, title string) {
	t.Helper()
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localeDir, "title.txt"), []byte(title+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlinkLocale(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func outsideLocale(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	shared := t.TempDir()
	writeTitle(t, shared, "Titre partagé")
	symlinkLocale(t, shared, filepath.Join(dir, "fr-FR"))
	return dir
}

func TestReadFollowsLocaleSymlinkedInsideTheTree(t *testing.T) {
	dir := t.TempDir()
	writeTitle(t, filepath.Join(dir, "fr-CA"), "Titre")
	symlinkLocale(t, filepath.Join(dir, "fr-CA"), filepath.Join(dir, "fr-FR"))

	tr, err := Read(dir)
	if err != nil {
		t.Fatalf("a locale symlinked inside the tree was refused: %v", err)
	}
	l, ok := tr["fr-FR"]
	if !ok {
		t.Fatalf("the symlinked locale is missing from the tree, so --prune would delete its Listing: %+v", tr)
	}
	if v, _ := l.Get(listing.Title); v != "Titre" {
		t.Errorf("title = %q, want the target's contents", v)
	}
	if _, ok := tr["fr-CA"]; !ok {
		t.Errorf("the link's target locale is missing from the tree: %+v", tr)
	}
}

func TestReadRefusesLocaleSymlinkedOutsideTheTree(t *testing.T) {
	dir := outsideLocale(t)

	tr, err := Read(dir)
	if err == nil {
		t.Fatalf("Read followed a locale symlinked out of the tree with the hatch unset: %+v", tr)
	}
	if !strings.Contains(err.Error(), "fr-FR") {
		t.Errorf("the refusal does not name the locale path: %v", err)
	}
	if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", pathguard.EnvAllowExternalSymlinks, err)
	}
}

func TestReadFollowsLocaleSymlinkedOutsideUnderTheHatch(t *testing.T) {
	dir := outsideLocale(t)
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	tr, err := Read(dir)
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	if v, _ := tr["fr-FR"].Get(listing.Title); v != "Titre partagé" {
		t.Errorf("the shared locale was not read back: %+v", tr)
	}
	if n := strings.Count(notes.String(), "NOTE: "); n != 1 {
		t.Errorf("want exactly one NOTE line for the escaping locale, got %d: %q", n, notes.String())
	}
	if !strings.Contains(notes.String(), "fr-FR") {
		t.Errorf("the NOTE does not name the locale path: %q", notes.String())
	}
}

func TestReadRefusesDanglingLocaleSymlink(t *testing.T) {
	dir := t.TempDir()
	symlinkLocale(t, filepath.Join(dir, "gone"), filepath.Join(dir, "fr-FR"))

	tr, err := Read(dir)
	if err == nil {
		t.Fatalf("a dangling locale symlink was skipped instead of refused: %+v", tr)
	}
	if !strings.Contains(err.Error(), "fr-FR") {
		t.Errorf("the error does not name the locale path: %v", err)
	}
}

func TestReadRefusesLocaleSymlinkedAtAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkLocale(t, filepath.Join(dir, "notes.md"), filepath.Join(dir, "fr-FR"))

	tr, err := Read(dir)
	if err == nil {
		t.Fatalf("a locale symlinked at a file was skipped instead of refused: %+v", tr)
	}
	if !strings.Contains(err.Error(), "fr-FR") {
		t.Errorf("the error does not name the locale path: %v", err)
	}
}
