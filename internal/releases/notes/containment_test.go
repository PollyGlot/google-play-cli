package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The exfiltration the PRD names (#459 story 5): a repo ships a notes directory
// where fr-FR.txt is a symlink to a private file. Uploaded with the operator's
// credentials, it would land in a public store listing.
func TestLoadRefusesEscapingSymlink(t *testing.T) {
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	const secretBody = "SECRET-KEY-MATERIAL"
	if err := os.WriteFile(secret, []byte(secretBody), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeNote(t, dir, "en-US.txt", "legit notes")
	if err := os.Symlink(secret, filepath.Join(dir, "fr-FR.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Zero reads outside the root: record every path the loader actually opens.
	var read []string
	restore := spyReads(t, &read)
	defer restore()

	out, err := Load(Opts{Dir: dir, DefaultLanguage: "en-US"})
	if err == nil {
		t.Fatalf("Load accepted an escaping symlink, returning %+v", out)
	}
	if !strings.Contains(err.Error(), "fr-FR.txt") {
		t.Errorf("the error does not name the offending file: %v", err)
	}
	for _, p := range read {
		if strings.HasPrefix(p, secretDir) || strings.Contains(p, "id_rsa") {
			t.Errorf("the loader read outside the root: %s", p)
		}
	}
	// And the secret must not have reached the payload either way.
	for _, n := range out {
		if strings.Contains(n.Text, secretBody) {
			t.Fatalf("secret material reached the release notes: %+v", n)
		}
	}
}

// A symlink that stays INSIDE the directory is legitimate (a repo may point
// pt-PT.txt at pt-BR.txt) and must keep working: containment is about escape,
// not about symlinks.
func TestLoadAllowsInternalSymlink(t *testing.T) {
	dir := t.TempDir()
	writeNote(t, dir, "pt-BR.txt", "notas")
	if err := os.Symlink(filepath.Join(dir, "pt-BR.txt"), filepath.Join(dir, "pt-PT.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	out, err := Load(Opts{Dir: dir, DefaultLanguage: "en-US"})
	if err != nil {
		t.Fatalf("an internal symlink was refused: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want both locales, got %+v", out)
	}
}

// The whole tree reached through a symlink: the "legitimate symlinked
// checkout" case of story 7.
func TestLoadFullySymlinkedRootPasses(t *testing.T) {
	real := t.TempDir()
	writeNote(t, real, "en-US.txt", "notes")

	link := filepath.Join(t.TempDir(), "notes-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	out, err := Load(Opts{Dir: link, DefaultLanguage: "en-US"})
	if err != nil {
		t.Fatalf("a fully symlinked notes directory was refused: %v", err)
	}
	if len(out) != 1 || out[0].Text != "notes" {
		t.Errorf("unexpected result through the symlinked root: %+v", out)
	}
}

func writeNote(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spyReads swaps the package's filesystem seam for one that records every path
// read, so a test can assert not just "refused" but "never touched".
func spyReads(t *testing.T, into *[]string) func() {
	t.Helper()
	origStat, origRead := osStat, osReadFile
	osStat = func(p string) (os.FileInfo, error) {
		*into = append(*into, p)
		return origStat(p)
	}
	osReadFile = func(p string) ([]byte, error) {
		*into = append(*into, p)
		return origRead(p)
	}
	return func() { osStat, osReadFile = origStat, origRead }
}
