package notes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

// sharedNotes builds the monorepo layout the hatch exists for: a release-notes
// directory whose `fr-FR.txt` is a symlink at a translation file shared with
// another tree. It returns the notes directory.
func sharedNotes(t *testing.T) string {
	t.Helper()
	shared := t.TempDir()
	target := filepath.Join(shared, "fr-FR.txt")
	if err := os.WriteFile(target, []byte("Notes partagees"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeNote(t, dir, "en-US.txt", "legit notes")
	if err := os.Symlink(target, filepath.Join(dir, "fr-FR.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return dir
}

// Default: refused, and the message names the way back so a monorepo operator
// is not left guessing.
func TestLoadRefusalNamesTheHatch(t *testing.T) {
	dir := sharedNotes(t)

	_, err := Load(Opts{Dir: dir, DefaultLanguage: "en-US"})
	if err == nil {
		t.Fatal("Load followed a note symlinked out of the tree with the hatch unset")
	}
	if !strings.Contains(err.Error(), pathguard.EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", pathguard.EnvAllowExternalSymlinks, err)
	}
}

// Under the opt-in the shared note is loaded again, with a NOTE on stderr
// saying which file left the tree.
func TestLoadFollowsSharedNoteUnderTheHatch(t *testing.T) {
	dir := sharedNotes(t)
	var stderr bytes.Buffer
	t.Cleanup(pathguard.SetNoteWriter(&stderr))
	t.Setenv(pathguard.EnvAllowExternalSymlinks, "1")

	out, err := Load(Opts{Dir: dir, DefaultLanguage: "en-US"})
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	var got string
	for _, n := range out {
		if n.Locale == "fr-FR" {
			got = n.Text
		}
	}
	if got != "Notes partagees" {
		t.Errorf("the shared note was not loaded, got %q from %+v", got, out)
	}
	if !strings.Contains(stderr.String(), "NOTE: ") || !strings.Contains(stderr.String(), "fr-FR.txt") {
		t.Errorf("stderr does not report the file that left the tree: %q", stderr.String())
	}
}
