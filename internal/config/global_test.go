package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

func tmpConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func TestGlobal_addAndSaveLoad_roundTrips(t *testing.T) {
	path := tmpConfigPath(t)

	g, err := config.LoadGlobalOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty (initial): %v", err)
	}
	g.AddAccount("playci")
	if err := g.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.LoadGlobalOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty (after save): %v", err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("Accounts = %v, want 1 entry", got.Accounts)
	}
	if got.Accounts[0].Name != "playci" {
		t.Errorf("Accounts[0].Name = %q, want %q", got.Accounts[0].Name, "playci")
	}
}

func TestGlobal_setActive_transitionsExactlyOneAccount(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("alpha")
	g.AddAccount("beta")
	g.AddAccount("gamma")

	if err := g.SetActive("beta"); err != nil {
		t.Fatalf("SetActive(beta): %v", err)
	}
	a, ok := g.Active()
	if !ok || a.Name != "beta" {
		t.Fatalf("Active() = (%+v, %v), want (beta, true)", a, ok)
	}

	if err := g.SetActive("gamma"); err != nil {
		t.Fatalf("SetActive(gamma): %v", err)
	}
	activeCount := 0
	for _, ac := range g.Accounts {
		if ac.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("active count = %d, want exactly 1", activeCount)
	}
	a, ok = g.Active()
	if !ok || a.Name != "gamma" {
		t.Errorf("Active() = (%+v, %v), want (gamma, true)", a, ok)
	}
}

func TestGlobal_setActive_unknownAccount_returnsErr(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("alpha")
	if err := g.SetActive("missing"); !errors.Is(err, config.ErrUnknownAccount) {
		t.Errorf("SetActive(missing) = %v, want ErrUnknownAccount", err)
	}
}

func TestGlobal_loadGlobalOrEmpty_missingFile_returnsEmptyGlobal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist.json")
	g, err := config.LoadGlobalOrEmpty(missing)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty (missing): %v", err)
	}
	if len(g.Accounts) != 0 {
		t.Errorf("Accounts = %v, want empty", g.Accounts)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadGlobalOrEmpty must not create the file lazily, got Stat err %v", err)
	}
}

func TestGlobal_addAccount_doesNotDuplicate(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("ci")
	g.AddAccount("ci")
	g.AddAccount("ci")
	if got := len(g.Accounts); got != 1 {
		t.Errorf("AddAccount called 3× → len = %d, want 1", got)
	}
}

func TestGlobal_removeAccount_removesEntry(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("alpha")
	g.AddAccount("beta")
	g.AddAccount("gamma")

	if err := g.RemoveAccount("beta"); err != nil {
		t.Fatalf("RemoveAccount(beta): %v", err)
	}
	if got := len(g.Accounts); got != 2 {
		t.Fatalf("len(Accounts) = %d after remove, want 2", got)
	}
	for _, a := range g.Accounts {
		if a.Name == "beta" {
			t.Errorf("beta still present after RemoveAccount: %+v", g.Accounts)
		}
	}
}

func TestGlobal_removeAccount_unknownReturnsErr(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("alpha")
	if err := g.RemoveAccount("missing"); !errors.Is(err, config.ErrUnknownAccount) {
		t.Errorf("RemoveAccount(missing) = %v, want ErrUnknownAccount", err)
	}
}

func TestGlobal_removeAccount_activeLeavesNoActive(t *testing.T) {
	g := &config.Global{}
	g.AddAccount("alpha")
	g.AddAccount("beta")
	if err := g.SetActive("beta"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := g.RemoveAccount("beta"); err != nil {
		t.Fatalf("RemoveAccount(beta): %v", err)
	}
	if _, ok := g.Active(); ok {
		t.Errorf("Active() should return false after removing the active account")
	}
}
