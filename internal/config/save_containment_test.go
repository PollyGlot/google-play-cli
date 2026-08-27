package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// Global.Save stages through `<path>.tmp` before renaming. That sibling is a
// name nothing owns: pre-place it as a symlink and a plain WriteFile follows
// the link, so saving the account registry overwrites the link's target with
// the operator's own rights. Same shape as the editpin pin file, same guard
// (PRD #459 / slice #461).
func TestGlobal_Save_refusesSymlinkedTmpPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	victim := filepath.Join(t.TempDir(), "id_rsa")
	const victimContent = "PRIVATE-VICTIM-CONTENT"
	if err := os.WriteFile(victim, []byte(victimContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	g := &config.Global{}
	g.AddAccount("ci")
	err := g.Save(context.Background(), config.OSFS{}, path)
	if err == nil {
		t.Error("Save accepted a symlinked .tmp staging path")
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

// The guard must not break the normal save, including into a config dir that
// does not exist yet (lazy creation by `auth login`) and one reached through a
// symlinked parent (macOS /tmp is itself a symlink).
func TestGlobal_Save_stillWritesThroughSymlinkedParent(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "cfg-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(link, "gplay", "config.json")

	g := &config.Global{}
	g.AddAccount("ci")
	if err := g.Save(context.Background(), config.OSFS{}, path); err != nil {
		t.Fatalf("Save through a symlinked parent was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "gplay", "config.json")); err != nil {
		t.Errorf("the config was not written: %v", err)
	}
	// The staging file must not survive a successful save.
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the .tmp staging file was left behind: %v", err)
	}
}
