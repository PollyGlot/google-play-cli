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

// sharedTranslations builds the monorepo layout the hatch exists for: a
// metadata tree whose `en-US/title.txt` is a symlink at a translation file
// kept in a shared directory outside the tree. It returns the tree root.
func sharedTranslations(t *testing.T) string {
	t.Helper()
	shared := t.TempDir()
	target := filepath.Join(shared, "title.txt")
	if err := os.WriteFile(target, []byte("Shared title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	localeDir := filepath.Join(dir, "en-US")
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(localeDir, "title.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dir
}

// Default: the layout is refused, and the message says which environment
// variable brings it back. Without that sentence a monorepo operator reads
// only that gplay broke.
func TestReadRefusesSharedLocaleAndNamesTheHatch(t *testing.T) {
	dir := sharedTranslations(t)

	_, err := Read(dir)
	if err == nil {
		t.Fatal("Read followed a field file symlinked out of the tree with the hatch unset")
	}
	if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", pathguard.EnvAllowExternalSymlinks, err)
	}
}

// Under the opt-in the pre-hardening behaviour is back: the shared translation
// is read, and stderr carries a NOTE naming what left the tree.
func TestReadFollowsSharedLocaleUnderTheHatch(t *testing.T) {
	dir := sharedTranslations(t)
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	tr, err := Read(dir)
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	l, ok := tr["en-US"]
	if !ok {
		t.Fatalf("the shared locale is missing from the tree: %+v", tr)
	}
	if v, _ := l.Get(listing.Title); v != "Shared title" {
		t.Errorf("title = %q, want the shared file's contents", v)
	}
	if !strings.Contains(notes.String(), "NOTE: ") {
		t.Errorf("nothing was reported on stderr for a path that left the tree: %q", notes.String())
	}
}

// The hatch covers the operator's own tree, never the API's data: on the write
// side the locale is a string the Play API returned, so it stays contained
// even with the variable set. This is the line that keeps the hatch from
// reopening the traversal the hardening closed.
func TestWriteStaysContainedUnderTheHatch(t *testing.T) {
	dir := t.TempDir()
	var notes bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&notes))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	// A locale directory pre-placed as a symlink out of the tree: exactly the
	// shape the hatch forgives on the read side.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "en-US")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr := listing.Tree{"en-US": listingWithTitle(t, "en-US", "From the store")}

	if err := Write(dir, tr); err == nil {
		t.Fatal("Write followed a symlink out of the tree because the hatch was set")
	} else if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not explain that the hatch does not cover an API-derived path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "title.txt")); err == nil {
		t.Error("store content was written outside the tree")
	}
	if notes.Len() != 0 {
		t.Errorf("a refusal must not emit a NOTE, got %q", notes.String())
	}
}

// A locale name the API returned that traverses is refused whatever the
// environment says: Segment runs before any containment decision.
func TestWriteRefusesTraversingAPILocaleUnderTheHatch(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(pathguard.SetNoteWriter(&bytes.Buffer{}))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	tr := listing.Tree{"../escaped": listingWithTitle(t, "../escaped", "From the store")}

	if err := Write(dir, tr); err == nil {
		t.Fatal("a traversing API locale was accepted because the hatch was set")
	}
}
