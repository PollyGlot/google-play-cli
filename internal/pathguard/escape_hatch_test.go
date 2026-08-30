package pathguard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// monorepo builds the layout the hatch exists for: a tree whose `en-US`
// directory is a symlink at a shared asset directory living outside it. It
// returns the containment root, the escaping path inside the tree, and the
// real file it resolves to.
func monorepo(t *testing.T) (root, escaping, real string) {
	t.Helper()
	shared := t.TempDir()
	real = filepath.Join(shared, "title.txt")
	if err := os.WriteFile(real, []byte("Shared title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(shared, filepath.Join(dir, "en-US")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return mustRoot(t, dir), filepath.Join(dir, "en-US", "title.txt"), real
}

// captureNotes points the NOTE stream at a buffer for the duration of the test.
func captureNotes(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	restore := SetNoteWriter(&buf)
	t.Cleanup(restore)
	return &buf
}

// Case 1: closed by default. An untouched environment keeps the refusal the
// hardening introduced, so the hatch costs nothing to anyone who has not asked
// for it.
func TestContainUserPathRefusesEscapeByDefault(t *testing.T) {
	root, escaping, _ := monorepo(t)
	notes := captureNotes(t)

	if _, err := ContainUserPath(root, escaping); err == nil {
		t.Fatal("an escaping symlink was accepted with the hatch unset")
	} else {
		assertUsage(t, err)
	}
	if notes.Len() != 0 {
		t.Errorf("a refusal must not emit a NOTE, got %q", notes.String())
	}
}

// Case 4: the refusal names the hatch. A monorepo operator whose working
// layout just broke has to be able to read what to do out of the message, not
// only what went wrong.
func TestContainUserPathRefusalNamesTheHatch(t *testing.T) {
	root, escaping, _ := monorepo(t)

	_, err := ContainUserPath(root, escaping)
	if err == nil {
		t.Fatal("an escaping symlink was accepted with the hatch unset")
	}
	msg := err.Error()
	if !strings.Contains(msg, EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not name %s: %v", EnvAllowExternalSymlinks, err)
	}
	if !strings.Contains(msg, "title.txt") {
		t.Errorf("the refusal does not name the offending path: %v", err)
	}
}

// Case 2: under the opt-in the pre-hardening behaviour comes back, and stderr
// says out loud that a path left the tree.
func TestContainUserPathFollowsEscapeUnderTheHatch(t *testing.T) {
	root, escaping, real := monorepo(t)
	notes := captureNotes(t)
	t.Setenv(EnvAllowExternalSymlinks, "1")

	got, err := ContainUserPath(root, escaping)
	if err != nil {
		t.Fatalf("the hatch did not lift the refusal: %v", err)
	}
	// The NAMED path comes back, not the resolved one: the caller opens what it
	// asked for, exactly as inside the tree.
	if got != escaping {
		t.Errorf("ContainUserPath = %q, want the named path %q", got, escaping)
	}
	note := notes.String()
	if !strings.HasPrefix(note, "NOTE: ") {
		t.Errorf("want a NOTE line on stderr, got %q", note)
	}
	for _, want := range []string{"title.txt", real, EnvAllowExternalSymlinks} {
		if !strings.Contains(note, want) {
			t.Errorf("the NOTE does not mention %q: %s", want, note)
		}
	}

	// One symlinked directory is contained once per file underneath it: the
	// same path must not print the same fact twice.
	before := notes.Len()
	if _, err := ContainUserPath(root, escaping); err != nil {
		t.Fatal(err)
	}
	if notes.Len() != before {
		t.Errorf("the NOTE repeated for the same path: %s", notes.String())
	}
}

// The hatch changes nothing for a path that never leaves the tree: no refusal,
// and no NOTE either (a NOTE means "this one actually went outside").
func TestContainUserPathStaysSilentInsideTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)
	notes := captureNotes(t)
	t.Setenv(EnvAllowExternalSymlinks, "1")

	if _, err := ContainUserPath(root, filepath.Join(dir, "en-US", "title.txt")); err != nil {
		t.Fatalf("a path inside the root was rejected: %v", err)
	}
	if notes.Len() != 0 {
		t.Errorf("a contained path must not emit a NOTE, got %q", notes.String())
	}
}

// Case 3, the line that keeps the hatch from reopening the vector: a path
// built from API data goes through Contain, which the environment cannot
// relax. A hostile locale is refused whether or not the operator opted in.
func TestContainIgnoresTheHatchForAPIDerivedPaths(t *testing.T) {
	root, escaping, _ := monorepo(t)
	notes := captureNotes(t)
	t.Setenv(EnvAllowExternalSymlinks, "1")

	_, err := Contain(root, escaping)
	if err == nil {
		t.Fatal("Contain followed an escaping symlink because the hatch was set")
	}
	assertUsage(t, err)
	// And it says why the hatch did not apply, so an operator who set it does
	// not conclude the variable is broken.
	if !strings.Contains(err.Error(), EnvAllowExternalSymlinks) {
		t.Errorf("the refusal does not explain that the hatch does not cover it: %v", err)
	}
	if notes.Len() != 0 {
		t.Errorf("a strict refusal must not emit a NOTE, got %q", notes.String())
	}
}

// With the hatch unset, Contain's message must NOT advertise it: nothing the
// operator can set would make this path pass.
func TestContainDoesNotAdvertiseTheHatch(t *testing.T) {
	root, escaping, _ := monorepo(t)

	_, err := Contain(root, escaping)
	if err == nil {
		t.Fatal("Contain accepted an escaping symlink")
	}
	if strings.Contains(err.Error(), EnvAllowExternalSymlinks) {
		t.Errorf("the strict refusal advertises a hatch that does not cover it: %v", err)
	}
}

// The hatch follows a link OUT of the tree; it does not let a path climb above
// the root. Keeping the two apart is what stops "monorepo layouts work again"
// from becoming "containment is off".
func TestContainUserPathStillRefusesTraversalUnderTheHatch(t *testing.T) {
	dir := t.TempDir()
	root := mustRoot(t, dir)
	captureNotes(t)
	t.Setenv(EnvAllowExternalSymlinks, "1")

	cases := []string{
		filepath.Join(dir, "..", "escaped.json"),
		filepath.Join(dir, "..", "..", "etc", "passwd"),
		filepath.Join(dir, "sub", "..", "..", "escaped.txt"),
	}
	for _, p := range cases {
		if _, err := ContainUserPath(root, p); err == nil {
			t.Errorf("the hatch allowed a traversal above the root: %q", p)
		} else {
			assertUsage(t, err)
		}
	}
}

// Falsy and unrecognised values leave containment closed: only the truthy
// spellings GPLAY_READONLY already uses count as an opt-in.
func TestContainUserPathHatchValues(t *testing.T) {
	open := []string{"1", "true", "TRUE", "yes", "on", " on "}
	closed := []string{"", "0", "false", "no", "off", "maybe"}

	for _, v := range open {
		root, escaping, _ := monorepo(t)
		captureNotes(t)
		t.Setenv(EnvAllowExternalSymlinks, v)
		if _, err := ContainUserPath(root, escaping); err != nil {
			t.Errorf("%s=%q did not open the hatch: %v", EnvAllowExternalSymlinks, v, err)
		}
	}
	for _, v := range closed {
		root, escaping, _ := monorepo(t)
		captureNotes(t)
		t.Setenv(EnvAllowExternalSymlinks, v)
		if _, err := ContainUserPath(root, escaping); err == nil {
			t.Errorf("%s=%q opened the hatch; only truthy values may", EnvAllowExternalSymlinks, v)
		}
	}
}

// A checkout reached through a symlink (macOS /var -> /private/var, a linked
// worktree) must not be mistaken for an escape by the hatch's own bookkeeping:
// a path under it is contained, and a ".." above it is still refused.
func TestContainUserPathUnderASymlinkedRoot(t *testing.T) {
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
		t.Fatal(err)
	}
	captureNotes(t)
	t.Setenv(EnvAllowExternalSymlinks, "1")

	if _, err := ContainUserPath(root, filepath.Join(link, "default.txt")); err != nil {
		t.Errorf("a file inside a symlinked root was rejected: %v", err)
	}
	if _, err := ContainUserPath(root, filepath.Join(link, "..", "escaped.txt")); err == nil {
		t.Error("the hatch let a traversal out of a symlinked root, where every path resolves")
	}
}
