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
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the .tmp staging file was left behind: %v", err)
	}
}
