package editpin_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Story 6: the package name flows into `.gplay/edit-<package>.json`. It comes
// from --package or from .gplay/config.json, and nothing upstream forbids a
// separator, so a crafted one would write outside .gplay/.
func TestWriteRefusesTraversingPackage(t *testing.T) {
	repo := t.TempDir()
	gplayDir := filepath.Join(repo, ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bad := []string{"../../etc/cron.d/x", "..", "/absolute/pkg", "sub/pkg"}
	for _, pkg := range bad {
		err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-1")
		if err == nil {
			t.Errorf("Write accepted package %q", pkg)
			continue
		}
		var ue *exit.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("package %q: want a usage error (exit 2), got %T: %v", pkg, err, err)
		}
	}

	// Nothing was written anywhere: the .gplay dir is still empty.
	entries, err := os.ReadDir(gplayDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused package still produced files: %v", entries)
	}
}

func TestLookupAndClearRefuseTraversingPackage(t *testing.T) {
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := editpin.Lookup(config.OSFS{}, gplayDir, "../escape"); err == nil {
		t.Error("Lookup accepted a traversing package")
	}
	if err := editpin.Clear(config.OSFS{}, gplayDir, "../escape"); err == nil {
		t.Error("Clear accepted a traversing package")
	}
}

// A real Android package name must keep round-tripping, including through a
// symlinked repo (story 7): containment is a guard, not a new restriction.
func TestRoundTripThroughSymlinkedGplayDir(t *testing.T) {
	real := t.TempDir()
	gplayDir := filepath.Join(real, ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked := filepath.Join(link, ".gplay")

	const pkg = "com.example.app"
	if err := editpin.Write(config.OSFS{}, linked, pkg, "edit-42"); err != nil {
		t.Fatalf("Write through a symlinked .gplay was refused: %v", err)
	}
	pin, ok, err := editpin.Lookup(config.OSFS{}, linked, pkg)
	if err != nil || !ok {
		t.Fatalf("Lookup through a symlinked .gplay failed: ok=%v err=%v", ok, err)
	}
	if pin.EditID != "edit-42" {
		t.Errorf("EditID = %q, want edit-42", pin.EditID)
	}
	if err := editpin.Clear(config.OSFS{}, linked, pkg); err != nil {
		t.Fatalf("Clear through a symlinked .gplay failed: %v", err)
	}
}

// Write must still create .gplay/ on first use: containment cannot depend on
// the directory already existing.
func TestWriteCreatesMissingGplayDir(t *testing.T) {
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	if err := editpin.Write(config.OSFS{}, gplayDir, "com.example.app", "edit-1"); err != nil {
		t.Fatalf("Write into a missing .gplay dir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gplayDir, "edit-com.example.app.json")); err != nil {
		t.Errorf("the pin was not written: %v", err)
	}
}

// The atomic write stages through `<pin>.tmp`, and that sibling is a path the
// repo can pre-place: the `edit-*.json` glob `gplay init` gitignores does NOT
// cover the `.tmp` suffix, so a symlink named `edit-<pkg>.json.tmp` is
// committable. Without a guard on the staging path, WriteFile follows it and
// overwrites whatever it points at, with the operator's own rights.
func TestWriteRefusesSymlinkedTmpPath(t *testing.T) {
	repo := t.TempDir()
	gplayDir := filepath.Join(repo, ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "id_rsa")
	const victimContent = "PRIVATE-VICTIM-CONTENT"
	if err := os.WriteFile(victim, []byte(victimContent), 0o600); err != nil {
		t.Fatal(err)
	}

	const pkg = "com.example.app"
	tmp := filepath.Join(gplayDir, editpin.FileName(pkg)+".tmp")
	if err := os.Symlink(victim, tmp); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-1")
	if err == nil {
		t.Error("Write accepted a symlinked .tmp staging path")
	} else {
		var ue *exit.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("want a usage error (exit 2), got %T: %v", err, err)
		}
	}

	got, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("the victim file is gone: %v", readErr)
	}
	if string(got) != victimContent {
		t.Errorf("the victim file was overwritten through the .tmp symlink:\n%s", got)
	}
}

// A .tmp symlink pointing back INSIDE the .gplay dir passes containment (its
// target is in the root) yet still writes to a file Write did not name. The
// leaf-symlink refusal is what covers it.
func TestWriteRefusesTmpSymlinkPointingInsideTheRoot(t *testing.T) {
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	if err := os.MkdirAll(gplayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(gplayDir, "config.json")
	const keep = `{"package":"com.example.app"}`
	if err := os.WriteFile(inside, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}

	const pkg = "com.example.app"
	tmp := filepath.Join(gplayDir, editpin.FileName(pkg)+".tmp")
	if err := os.Symlink(inside, tmp); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-1"); err == nil {
		t.Error("Write accepted a .tmp symlink aimed inside the root")
	}
	got, err := os.ReadFile(inside)
	if err != nil {
		t.Fatalf("the project config is gone: %v", err)
	}
	if string(got) != keep {
		t.Errorf("the project config was clobbered through the .tmp symlink:\n%s", got)
	}
}
