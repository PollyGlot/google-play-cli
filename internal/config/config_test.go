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

func TestConfig_addAndSaveLoad_roundTrips(t *testing.T) {
	path := tmpConfigPath(t)

	c, err := config.LoadOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadOrEmpty (initial): %v", err)
	}
	c.AddAccount("playci")
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.LoadOrEmpty(path)
	if err != nil {
		t.Fatalf("LoadOrEmpty (after save): %v", err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("Accounts = %v, want 1 entry", got.Accounts)
	}
	if got.Accounts[0].Name != "playci" {
		t.Errorf("Accounts[0].Name = %q, want %q", got.Accounts[0].Name, "playci")
	}
}

func TestConfig_setActive_transitionsExactlyOneAccount(t *testing.T) {
	c := &config.Config{}
	c.AddAccount("alpha")
	c.AddAccount("beta")
	c.AddAccount("gamma")

	if err := c.SetActive("beta"); err != nil {
		t.Fatalf("SetActive(beta): %v", err)
	}
	a, ok := c.Active()
	if !ok || a.Name != "beta" {
		t.Fatalf("Active() = (%+v, %v), want (beta, true)", a, ok)
	}

	if err := c.SetActive("gamma"); err != nil {
		t.Fatalf("SetActive(gamma): %v", err)
	}
	activeCount := 0
	for _, ac := range c.Accounts {
		if ac.Active {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("active count = %d, want exactly 1", activeCount)
	}
	a, ok = c.Active()
	if !ok || a.Name != "gamma" {
		t.Errorf("Active() = (%+v, %v), want (gamma, true)", a, ok)
	}
}

func TestConfig_setActive_unknownAccount_returnsErr(t *testing.T) {
	c := &config.Config{}
	c.AddAccount("alpha")
	if err := c.SetActive("missing"); !errors.Is(err, config.ErrUnknownAccount) {
		t.Errorf("SetActive(missing) = %v, want ErrUnknownAccount", err)
	}
}

func TestConfig_loadOrEmpty_missingFile_returnsEmptyConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist.json")
	c, err := config.LoadOrEmpty(missing)
	if err != nil {
		t.Fatalf("LoadOrEmpty (missing): %v", err)
	}
	if len(c.Accounts) != 0 {
		t.Errorf("Accounts = %v, want empty", c.Accounts)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LoadOrEmpty must not create the file lazily, got Stat err %v", err)
	}
}

func TestConfig_addAccount_doesNotDuplicate(t *testing.T) {
	c := &config.Config{}
	c.AddAccount("ci")
	c.AddAccount("ci")
	c.AddAccount("ci")
	if got := len(c.Accounts); got != 1 {
		t.Errorf("AddAccount called 3× → len = %d, want 1", got)
	}
}
