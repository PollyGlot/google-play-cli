package imagetree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// onePNG is the minimal byte prefix imagetree sniffs as a PNG.
var onePNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)

// A screenshot symlinked at a private file would be uploaded to a public store
// listing under the operator's credentials (PRD #459 story 5).
func TestReadRefusesEscapingImageSymlink(t *testing.T) {
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "private.png")
	if err := os.WriteFile(secret, onePNG, 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	imgDir := filepath.Join(dir, "en-US", "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(imgDir, "icon.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := imagetree.Read(dir); err == nil {
		t.Fatal("Read accepted an image symlinked outside the tree")
	} else if !strings.Contains(err.Error(), "icon.png") {
		t.Errorf("the error does not name the offending file: %v", err)
	}
}

// The higher-leverage variant: one symlinked locale directory would make every
// file under it escape. Two independent defences stop it, and the test asserts
// the OUTCOME both share: nothing from outside the tree ends up in the Tree.
//
// The first defence is upstream of containment: os.ReadDir reports a symlink
// with Lstat semantics, so DirEntry.IsDir() is false for a symlinked directory
// and Read skips it as a stray entry (the package's documented leniency). The
// containment check is the backstop for any path that does get walked.
func TestReadDoesNotFollowEscapingLocaleDirectory(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "images", "icon.png"), onePNG, 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "en-US")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr, err := imagetree.Read(dir)
	if err != nil {
		// Refusing outright is also acceptable; what must never happen is
		// silently returning the outside file.
		return
	}
	if len(tr) != 0 {
		t.Fatalf("Read pulled content in through a locale symlinked outside the tree: %+v", tr)
	}
}

// Story 7: a checkout reached entirely through a symlink keeps working.
func TestReadFullySymlinkedRootPasses(t *testing.T) {
	real := t.TempDir()
	imgDir := filepath.Join(real, "en-US", "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "icon.png"), onePNG, 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(t.TempDir(), "meta-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr, err := imagetree.Read(link)
	if err != nil {
		t.Fatalf("a fully symlinked image root was refused: %v", err)
	}
	if len(tr["en-US"][images.Icon]) != 1 {
		t.Errorf("the icon was not read through the symlinked root: %+v", tr)
	}
}

// Story 6: the locale keys of a Tree come from the Play API on the pull path.
// A crafted one must not write outside the target directory.
func TestWriteRefusesTraversingLocale(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "..", "escaped")

	tr := imagetree.Tree{
		"../escaped": {images.Icon: [][]byte{onePNG}},
	}
	if err := imagetree.Write(filepath.Join(dir, "out"), tr); err == nil {
		t.Fatal("Write accepted a traversing locale name")
	}
	if _, err := os.Stat(victim); err == nil {
		t.Errorf("Write created %s, outside the target directory", victim)
	}
}

func TestWriteAcceptsOrdinaryLocales(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	tr := imagetree.Tree{"en-US": {images.Icon: [][]byte{onePNG}}}
	if err := imagetree.Write(dir, tr); err != nil {
		t.Fatalf("Write refused an ordinary locale: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "icon.png")); err != nil {
		t.Errorf("the icon was not written: %v", err)
	}
}
