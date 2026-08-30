package tree

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// The exfiltration the PRD names (#459 story 5), on the TEXT codec: a repo
// ships a metadata tree where en-US/title.txt is a symlink to a private file.
// Read would load the secret into the Listing, and `metadata apply` would
// publish it to a store listing under the operator's own credentials.
func TestReadRefusesEscapingFieldSymlink(t *testing.T) {
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	const secretBody = "SECRET-KEY-MATERIAL"
	if err := os.WriteFile(secret, []byte(secretBody), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	localeDir := filepath.Join(dir, "en-US")
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(localeDir, "title.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Zero reads outside the root: record every path the codec actually opens.
	var read []string
	restore := spyReads(t, &read)
	defer restore()

	tr, err := Read(dir)
	if err == nil {
		t.Fatalf("Read accepted an escaping symlink, returning %+v", tr)
	}
	if !strings.Contains(err.Error(), "title.txt") {
		t.Errorf("the error does not name the offending file: %v", err)
	}
	for _, p := range read {
		if strings.HasPrefix(p, secretDir) || strings.Contains(p, "id_rsa") {
			t.Errorf("the codec read outside the root: %s", p)
		}
	}
	for loc, l := range tr {
		if v, _ := l.Get(listing.Title); strings.Contains(v, secretBody) {
			t.Fatalf("secret material reached the listing for %q", loc)
		}
	}
}

// A symlink that stays INSIDE the tree is legitimate and must keep working:
// containment is about escape, not about symlinks.
func TestReadAllowsInternalFieldSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pt-BR")
	dst := filepath.Join(dir, "pt-PT")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "title.txt"), []byte("titulo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "title.txt"), filepath.Join(dst, "title.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr, err := Read(dir)
	if err != nil {
		t.Fatalf("an internal symlink was refused: %v", err)
	}
	if len(tr) != 2 {
		t.Fatalf("want both locales, got %+v", tr)
	}
}

// Story 7: a checkout reached entirely through a symlink keeps working (on
// macOS even t.TempDir() is under a symlinked /tmp, so this is the common case
// in disguise).
func TestReadFullySymlinkedRootPasses(t *testing.T) {
	real := t.TempDir()
	localeDir := filepath.Join(real, "en-US")
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localeDir, "title.txt"), []byte("Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(t.TempDir(), "metadata-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr, err := Read(link)
	if err != nil {
		t.Fatalf("a fully symlinked metadata root was refused: %v", err)
	}
	if v, _ := tr["en-US"].Get(listing.Title); v != "Title" {
		t.Errorf("the title was not read through the symlinked root: %+v", tr)
	}
}

// Story 6: on the pull path the locale keys come from the Play API, a string
// gplay did not author. A traversing one must not write outside the tree.
func TestWriteRefusesTraversingLocale(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "escaped")

	tr := listing.Tree{"../escaped": listingWithTitle(t, "../escaped", "pwned")}
	if err := Write(filepath.Join(dir, "out"), tr); err == nil {
		t.Fatal("Write accepted a traversing locale name")
	}
	if _, err := os.Stat(victim); err == nil {
		t.Errorf("Write created %s, outside the target directory", victim)
	}
}

// A field file pre-placed as a symlink to a file outside the tree must not be
// written THROUGH: os.WriteFile follows the link and would overwrite the
// victim with store text on `metadata pull`.
func TestWriteRefusesSymlinkedFieldFile(t *testing.T) {
	victimDir := t.TempDir()
	victim := filepath.Join(victimDir, "id_rsa")
	const original = "PRIVATE-KEY"
	if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	localeDir := filepath.Join(dir, "en-US")
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(localeDir, "title.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tr := listing.Tree{"en-US": listingWithTitle(t, "en-US", "Overwritten")}
	if err := Write(dir, tr); err == nil {
		t.Fatal("Write wrote through a symlinked field file")
	}
	b, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != original {
		t.Fatalf("the victim outside the tree was overwritten: %q", b)
	}
}

func TestWriteAcceptsOrdinaryLocales(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	tr := listing.Tree{"en-US": listingWithTitle(t, "en-US", "Title")}
	if err := Write(dir, tr); err != nil {
		t.Fatalf("Write refused an ordinary locale: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "en-US", "title.txt"))
	if err != nil {
		t.Fatalf("the title was not written: %v", err)
	}
	if string(b) != "Title\n" {
		t.Errorf("unexpected file body %q", b)
	}
}

func listingWithTitle(t *testing.T, loc, title string) listing.Listing {
	t.Helper()
	l := listing.NewListing(loc)
	l.Set(listing.Title, title)
	return l
}

// spyReads swaps the package's filesystem seams for ones that record every
// path read, so a test can assert not just "refused" but "never touched".
func spyReads(t *testing.T, into *[]string) func() {
	t.Helper()
	origReadDir, origReadFile := osReadDir, osReadFile
	osReadDir = func(p string) ([]fs.DirEntry, error) {
		*into = append(*into, p)
		return origReadDir(p)
	}
	osReadFile = func(p string) ([]byte, error) {
		*into = append(*into, p)
		return origReadFile(p)
	}
	return func() { osReadDir, osReadFile = origReadDir, origReadFile }
}
