package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// mustRoot builds a temp dir and returns both the raw and the resolved form.
// On macOS t.TempDir() sits under /var, itself a symlink to /private/var, which
// is exactly the "legitimately symlinked checkout" case containment must not
// break: keeping both forms makes that visible in the tests below.
func mustRoot(t *testing.T, dir string) string {
	t.Helper()
	root, err := Root(dir)
	if err != nil {
		t.Fatalf("Root(%q): %v", dir, err)
	}
	return root
}

func TestContainAcceptsPathsInsideRoot(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)

	inside := filepath.Join(dir, "fr-FR.txt")
	if err := os.WriteFile(inside, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "en-US", "images")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{inside, nested, dir, filepath.Join(dir, "does-not-exist-yet.png")} {
		if _, err := Contain(root, p); err != nil {
			t.Errorf("Contain(root, %q) rejected a legitimate path: %v", p, err)
		}
	}
}

// The whole point of resolving the ROOT too: a checkout reached through a
// symlink (or macOS's /var → /private/var) must keep working.
func TestContainFullySymlinkedRootPasses(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "default.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-to-repo")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	root, err := Root(link)
	if err != nil {
		t.Fatalf("Root(symlinked dir): %v", err)
	}
	// Addressed through the symlink, the file must be accepted: its resolved
	// form lands under the resolved root.
	if _, err := Contain(root, filepath.Join(link, "default.txt")); err != nil {
		t.Errorf("a fully symlinked root was rejected: %v", err)
	}
	// And addressed through the real path too: same file, same verdict.
	if _, err := Contain(root, filepath.Join(real, "default.txt")); err != nil {
		t.Errorf("the real path under a symlinked root was rejected: %v", err)
	}
}

func TestContainRefusesEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)

	// The exfiltration shape the PRD names: a file OUTSIDE the tree, reachable
	// through an innocuous-looking name inside it.
	outside := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(outside, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaping := filepath.Join(dir, "fr-FR.txt")
	if err := os.Symlink(outside, escaping); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Contain(root, escaping)
	if err == nil {
		t.Fatal("an escaping symlink was accepted")
	}
	assertUsage(t, err)
	if !strings.Contains(err.Error(), "fr-FR.txt") {
		t.Errorf("the error does not name the offending path: %v", err)
	}
}

// A symlinked DIRECTORY is the more dangerous case: it makes every file under
// it escape at once.
func TestContainRefusesEscapingSymlinkedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)

	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(dir, "en-US")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Contain(root, filepath.Join(dir, "en-US", "secret.png")); err == nil {
		t.Fatal("a file under an escaping symlinked directory was accepted")
	}
}

func TestContainRefusesDotDotTraversal(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)

	cases := []string{
		filepath.Join(dir, "..", "escaped.json"),
		filepath.Join(dir, "..", "..", "etc", "passwd"),
		filepath.Join(dir, "sub", "..", "..", "escaped.txt"),
	}
	for _, p := range cases {
		if _, err := Contain(root, p); err == nil {
			t.Errorf("Contain accepted a traversal: %q", p)
		} else {
			assertUsage(t, err)
		}
	}
}

// Rel, not a string prefix: a sibling whose name merely starts with the root's
// is outside it.
func TestContainRefusesSiblingWithSharedPrefix(t *testing.T) {
	parent := t.TempDir()
	inside := filepath.Join(parent, ".gplay")
	sibling := filepath.Join(parent, ".gplay-evil")
	for _, d := range []string{inside, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := mustRoot(t, inside)

	if _, err := Contain(root, filepath.Join(sibling, "config.json")); err == nil {
		t.Error("a sibling directory sharing the root's name prefix was accepted")
	}
}

// The write path needs paths that do not exist yet to be judged on their
// ancestors, not rejected outright.
func TestContainHandlesNotYetExistingPaths(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)

	if _, err := Contain(root, filepath.Join(dir, "en-US", "images", "icon.png")); err != nil {
		t.Errorf("a not-yet-existing path inside the root was rejected: %v", err)
	}
	if _, err := Contain(root, filepath.Join(dir, "..", "en-US", "icon.png")); err == nil {
		t.Error("a not-yet-existing path OUTSIDE the root was accepted")
	}
}

// Contain returns the path the caller NAMED (absolute), not the resolved one,
// so error messages and the actual open agree with the user's input.
func TestContainReturnsTheNamedPath(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)
	p := filepath.Join(dir, "default.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Contain(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("Contain returned %q, want the named path %q", got, p)
	}
}

func TestContainEmptyRootIsRefused(t *testing.T) {
	if _, err := Contain("", "/tmp/x"); err == nil {
		t.Error("an empty root was accepted; that would disable containment silently")
	}
}

func TestRootRejectsMissingDirectory(t *testing.T) {
	_, err := Root(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("Root accepted a missing directory")
	}
	if !os.IsNotExist(err) {
		t.Errorf("want a not-exist error the caller can recognise, got %v", err)
	}
}

func TestSegment(t *testing.T) {
	ok := []string{"com.example.app", "en-US", "fr", "icon.png", "1.png", "a b"}
	for _, name := range ok {
		if err := Segment("package", name); err != nil {
			t.Errorf("Segment rejected a legitimate name %q: %v", name, err)
		}
	}

	bad := []string{"", ".", "..", "../evil", "a/b", "/abs/path", "./x", "sub/", "x\x00y"}
	for _, name := range bad {
		err := Segment("package", name)
		if err == nil {
			t.Errorf("Segment accepted %q, which is not a plain file name", name)
			continue
		}
		assertUsage(t, err)
		if !strings.Contains(err.Error(), "package") {
			t.Errorf("the error for %q does not name the input: %v", name, err)
		}
	}
}

func TestJoinSegment(t *testing.T) {
	got, err := JoinSegment("locale", "/repo/.gplay", "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/repo/.gplay", "en-US"); got != want {
		t.Errorf("JoinSegment = %q, want %q", got, want)
	}
	if _, err := JoinSegment("locale", "/repo/.gplay", "../../etc"); err == nil {
		t.Error("JoinSegment accepted a traversing segment")
	}
}

// A containment refusal is bad INPUT, not a transient fault: it must map to the
// documented usage exit code (2, docs/DESIGN.md §9) so CI can tell the two
// apart without scraping the message.
func assertUsage(t *testing.T, err error) {
	t.Helper()
	var ue *exit.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("want a *exit.UsageError (exit 2), got %T: %v", err, err)
		return
	}
	if code := exit.For(err); code != 2 {
		t.Errorf("want exit code 2, got %d", code)
	}
}
