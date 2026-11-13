package imagetree

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

// failingBlob sniffs as a PNG, then fails its copy after writing part of it:
// a staged file that cannot be read back in full.
type failingBlob struct{}

func (failingBlob) head() ([]byte, error) { return []byte("\x89PNG\r\n\x1a\n"), nil }
func (failingBlob) copyTo(w io.Writer) error {
	_, _ = w.Write([]byte("\x89PNG partial"))
	return errors.New("read staged file: boom")
}

// TestWriteContained_failedCopyKeepsExistingImage asserts a copy that fails
// partway leaves the image already on disk intact, not truncated, and leaves
// no temporary file next to it.
func TestWriteContained_failedCopyKeepsExistingImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "icon.png")
	old := []byte("\x89PNG\r\n\x1a\nold icon")
	if err := os.WriteFile(p, old, filePerm); err != nil {
		t.Fatal(err)
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeContained(root, p, failingBlob{}); err == nil {
		t.Fatal("writeContained succeeded with a failing copy")
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, old) {
		t.Errorf("icon.png = %q after a failed copy, want the old bytes %q", got, old)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("dir holds %d entries after a failed copy, want only icon.png", len(entries))
	}
}

// TestWriteContained_replacesWithFilePerm asserts a successful write replaces
// the existing image and leaves it with filePerm.
func TestWriteContained_replacesWithFilePerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeContained(root, p, memBlob("new")); err != nil {
		t.Fatalf("writeContained: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" {
		t.Errorf("icon.png = %q, want %q", got, "new")
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != filePerm {
		t.Errorf("icon.png mode = %v, %v; want %v", fi.Mode().Perm(), err, filePerm)
	}
}
