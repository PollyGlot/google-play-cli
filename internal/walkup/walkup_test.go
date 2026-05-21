package walkup_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/walkup"
)

func TestFindFile_foundAtStart(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "marker"), "x")

	got, err := walkup.FindFile(root, "marker")
	if err != nil {
		t.Fatalf("FindFile: %v", err)
	}
	if got != root {
		t.Errorf("FindFile = %q, want %q", got, root)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func mustMkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return path
}

func TestFindFile_foundInAncestor(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "marker"), "x")
	deep := mustMkdir(t, filepath.Join(root, "a", "b", "c"))

	got, err := walkup.FindFile(deep, "marker")
	if err != nil {
		t.Fatalf("FindFile: %v", err)
	}
	if got != root {
		t.Errorf("FindFile = %q, want %q (root)", got, root)
	}
}

func TestFindFile_notFound_returnsErrNotExist(t *testing.T) {
	root := t.TempDir()
	_, err := walkup.FindFile(root, "no-such-file")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FindFile = %v, want os.ErrNotExist", err)
	}
}

// FindFileExcluding refuses to consider dirToExclude (and its ancestors) as
// candidates. Practical use: prevent walk-up from picking up a config that
// happens to live in $HOME, which would let a rogue ~/.gplay/config.json
// hijack every repo on the machine.
func TestFindFileExcluding_refusesMatchAtExcludedDir(t *testing.T) {
	root := t.TempDir()
	excluded := mustMkdir(t, filepath.Join(root, "home"))
	mustWriteFile(t, filepath.Join(excluded, "marker"), "x")

	_, err := walkup.FindFileExcluding(excluded, "marker", excluded)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FindFileExcluding (start == excluded) = %v, want os.ErrNotExist", err)
	}
}

func TestFindFileExcluding_refusesMatchAboveExcludedDir(t *testing.T) {
	root := t.TempDir()
	excluded := mustMkdir(t, filepath.Join(root, "home"))
	mustWriteFile(t, filepath.Join(root, "marker"), "x") // candidate sits ABOVE excluded
	startInsideExcluded := mustMkdir(t, filepath.Join(excluded, "deeper"))

	_, err := walkup.FindFileExcluding(startInsideExcluded, "marker", excluded)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FindFileExcluding (match above excluded) = %v, want os.ErrNotExist", err)
	}
}

func TestFindFileExcluding_findsMatchBelowExcludedDir(t *testing.T) {
	root := t.TempDir()
	excluded := mustMkdir(t, filepath.Join(root, "home"))
	pinnedRepo := mustMkdir(t, filepath.Join(root, "work", "repo"))
	mustWriteFile(t, filepath.Join(pinnedRepo, "marker"), "x")
	sub := mustMkdir(t, filepath.Join(pinnedRepo, "src", "a"))

	got, err := walkup.FindFileExcluding(sub, "marker", excluded)
	if err != nil {
		t.Fatalf("FindFileExcluding: %v", err)
	}
	if got != pinnedRepo {
		t.Errorf("FindFileExcluding = %q, want %q", got, pinnedRepo)
	}
}
