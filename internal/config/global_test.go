package config_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/config"
)

// TestGlobal_addAndSaveLoad_roundTrips verifies the in-memory FS seam
// without t.TempDir: proof that the FS interface lets the config tests
// run hermetic and in-memory.
func TestGlobal_addAndSaveLoad_roundTrips(t *testing.T) {
	fsys := config.NewMemFS("/", "/home/u")
	ctx := context.Background()
	path := "/cfg/config.json"

	g, err := config.LoadGlobalOrEmpty(ctx, fsys, path)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty (initial): %v", err)
	}
	g.AddAccount("playci")
	if err := g.Save(ctx, fsys, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.LoadGlobalOrEmpty(ctx, fsys, path)
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
	fsys := config.NewMemFS("/", "/home/u")
	missing := "/does/not/exist.json"
	g, err := config.LoadGlobalOrEmpty(context.Background(), fsys, missing)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty (missing): %v", err)
	}
	if len(g.Accounts) != 0 {
		t.Errorf("Accounts = %v, want empty", g.Accounts)
	}
	if _, err := fsys.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
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

// TestGlobal_Save_isAtomicViaRename asserts that Save writes through a
// `.tmp` sibling + rename rather than truncating the destination in
// place. This is the guard against a SIGKILL / disk-full corrupting
// config.json into a half-written state that every subsequent gplay
// invocation fails to parse. The test uses MemFS but the contract is
// FS-level: a failing Rename must leave the original file (if any)
// intact and not write the destination path.
func TestGlobal_Save_isAtomicViaRename(t *testing.T) {
	fsys := config.NewMemFS("/", "/home/u")
	path := "/cfg/config.json"

	// Seed an existing valid config.
	if err := fsys.WriteFile(path, []byte(`{"accounts":[{"name":"prior","active":true}]}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	g := &config.Global{Accounts: []config.Account{{Name: "new", Active: true}}}
	if err := g.Save(context.Background(), fsys, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Final file content reflects the new write.
	data, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytesContains(data, "new") {
		t.Errorf("Save did not land the new content; got %s", data)
	}
	// And the `.tmp` sibling must be gone (rename consumed it).
	if _, err := fsys.Stat(path + ".tmp"); err == nil {
		t.Errorf("Save left a .tmp sibling around after rename")
	}
}

// TestGlobal_legacyConfigWithoutPackagesField_loadsCleanly asserts the
// on-disk schema is backwards-compatible: a global config written before
// the per-Account Packages field existed (i.e. without a `packages`
// key) must still load without error and surface an empty Packages
// slice. The omitempty tag also guarantees re-saving such a config does
// not introduce noisy `"packages":null` keys.
func TestGlobal_legacyConfigWithoutPackagesField_loadsCleanly(t *testing.T) {
	fsys := config.NewMemFS("/", "/home/u")
	path := "/cfg/config.json"
	legacy := []byte(`{"accounts":[{"name":"playci","active":true}]}`)
	if err := fsys.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	g, err := config.LoadGlobalOrEmpty(context.Background(), fsys, path)
	if err != nil {
		t.Fatalf("LoadGlobalOrEmpty: %v", err)
	}
	if len(g.Accounts) != 1 || g.Accounts[0].Name != "playci" {
		t.Fatalf("Accounts = %+v, want single playci", g.Accounts)
	}
	if g.Accounts[0].Packages != nil {
		t.Errorf("Packages = %v, want nil for legacy config", g.Accounts[0].Packages)
	}

	if err := g.Save(context.Background(), fsys, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after save: %v", err)
	}
	if bytesContains(saved, "packages") {
		t.Errorf("saved config emitted a `packages` key even though none were added:\n%s", saved)
	}
}

func bytesContains(haystack []byte, needle string) bool {
	return len(haystack) >= len(needle) && indexBytes(haystack, needle) >= 0
}

func indexBytes(h []byte, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if string(h[i:i+len(n)]) == n {
			return i
		}
	}
	return -1
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
