package installskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedPin_isValidAndReviewable(t *testing.T) {
	pin, err := embeddedPin()
	if err != nil {
		t.Fatalf("the pin shipped in the binary must be valid: %v", err)
	}
	if pin.Repo != "PollyGlot/google-play-cli-skills" {
		t.Errorf("repo = %q", pin.Repo)
	}
	if !strings.HasPrefix(pin.URL, "https://") {
		t.Errorf("clone URL must be https, got %q", pin.URL)
	}
	if len(pin.Skills) == 0 {
		t.Error("the pinned pack is empty")
	}
}

func TestPin_validate(t *testing.T) {
	base := Pin{
		Repo: "o/r", URL: "https://example.test/r.git", Subdir: "skills",
		Commit: "0123456789abcdef0123456789abcdef01234567",
		Skills: []string{"a", "b"},
	}
	if err := base.validate(); err != nil {
		t.Fatalf("base pin should be valid: %v", err)
	}

	cases := map[string]func(p *Pin){
		// An abbreviated hash is a prefix match, so it does not pin one tree.
		"short commit":    func(p *Pin) { p.Commit = "0123456" },
		"branch name":     func(p *Pin) { p.Commit = "main" },
		"uppercase hash":  func(p *Pin) { p.Commit = strings.ToUpper(p.Commit) },
		"unsorted skills": func(p *Pin) { p.Skills = []string{"b", "a"} },
		"duplicate skill": func(p *Pin) { p.Skills = []string{"a", "a"} },
		// A traversing name would let the copy escape the target directory.
		"path traversal": func(p *Pin) { p.Skills = []string{".."} },
		"nested name":    func(p *Pin) { p.Skills = []string{"a/b"} },
		"no skills":      func(p *Pin) { p.Skills = nil },
		"no url":         func(p *Pin) { p.URL = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base
			p.Skills = append([]string{}, base.Skills...)
			mutate(&p)
			if err := p.validate(); err == nil {
				t.Errorf("%s should be rejected", name)
			}
		})
	}
}

// TestSwap_rollbackRestoresPreviousSkills drives the replacement engine
// directly: a failure partway through the pack must leave every skill exactly
// as it was, whether it had a previous version or not.
func TestSwap_rollbackRestoresPreviousSkills(t *testing.T) {
	target, stage, backup := t.TempDir(), t.TempDir(), t.TempDir()
	// Same filesystem is what production guarantees; t.TempDir gives us that.
	write(t, filepath.Join(target, "alpha", "SKILL.md"), "old alpha\n")
	write(t, filepath.Join(target, "unrelated", "SKILL.md"), "untouched\n")
	write(t, filepath.Join(stage, "alpha", "SKILL.md"), "new alpha\n")
	write(t, filepath.Join(stage, "beta", "SKILL.md"), "new beta\n")

	sw := &swap{target: target, backup: backup}
	if err := sw.install("alpha", filepath.Join(stage, "alpha")); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if got := readFile(t, filepath.Join(target, "alpha", "SKILL.md")); got != "new alpha\n" {
		t.Fatalf("alpha not swapped in: %q", got)
	}
	// "beta" is nowhere in the staging area: the rename fails, exactly as a
	// mid-pack filesystem error would.
	if err := sw.install("beta", filepath.Join(stage, "does-not-exist")); err == nil {
		t.Fatal("expected the second install to fail")
	}

	if errs := sw.rollback(); len(errs) != 0 {
		t.Fatalf("rollback reported errors: %v", errs)
	}
	if got := readFile(t, filepath.Join(target, "alpha", "SKILL.md")); got != "old alpha\n" {
		t.Errorf("previous alpha not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "beta")); !os.IsNotExist(err) {
		t.Error("a half-installed skill survived the rollback")
	}
	if got := readFile(t, filepath.Join(target, "unrelated", "SKILL.md")); got != "untouched\n" {
		t.Errorf("unrelated skill disturbed: %q", got)
	}
}

// A skill that did not exist before must be removed by the rollback, not left
// behind as a partial install.
func TestSwap_rollbackRemovesNewlyAddedSkill(t *testing.T) {
	target, stage, backup := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(stage, "alpha", "SKILL.md"), "new alpha\n")

	sw := &swap{target: target, backup: backup}
	if err := sw.install("alpha", filepath.Join(stage, "alpha")); err != nil {
		t.Fatalf("install: %v", err)
	}
	if errs := sw.rollback(); len(errs) != 0 {
		t.Fatalf("rollback reported errors: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(target, "alpha")); !os.IsNotExist(err) {
		t.Error("alpha should be gone after rollback")
	}
}

func TestVerifyTree_detectsEveryKindOfDrift(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "SKILL.md"), "body\n")
	write(t, filepath.Join(src, "nested", "a.md"), "nested\n")

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if err := verifyTree(src, dst); err != nil {
		t.Fatalf("a faithful copy must verify: %v", err)
	}

	t.Run("missing file", func(t *testing.T) {
		d := t.TempDir()
		if err := copyTree(src, filepath.Join(d, "x")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(d, "x", "nested", "a.md")); err != nil {
			t.Fatal(err)
		}
		assertVerifyFails(t, src, filepath.Join(d, "x"), "missing")
	})
	t.Run("altered content", func(t *testing.T) {
		d := t.TempDir()
		if err := copyTree(src, filepath.Join(d, "x")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(d, "x", "SKILL.md"), "tampered\n")
		assertVerifyFails(t, src, filepath.Join(d, "x"), "differs")
	})
	t.Run("extra file", func(t *testing.T) {
		d := t.TempDir()
		if err := copyTree(src, filepath.Join(d, "x")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(d, "x", "stowaway.md"), "extra\n")
		assertVerifyFails(t, src, filepath.Join(d, "x"), "unexpected")
	})
}

// A symlink in the pack would resolve against the user's filesystem at install
// time, so the copy refuses it instead of following it.
func TestCopyTree_refusesSymlinks(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	write(t, filepath.Join(src, "SKILL.md"), "body\n")
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyTree(src, dst); err == nil {
		t.Fatal("expected copyTree to refuse a symlink")
	}
}

func assertVerifyFails(t *testing.T, src, dst, want string) {
	t.Helper()
	err := verifyTree(src, dst)
	if err == nil {
		t.Fatalf("expected verification to fail (%s)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should mention %q, got: %v", want, err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
