package registry_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

func TestAdd_Has_findsAddedPackage(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true}}

	if err := registry.Add(&accounts, "playci", "com.example.app"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !registry.Has(accounts, "playci", "com.example.app") {
		t.Error("Has after Add: want true")
	}
}

func TestAdd_idempotent_doubleAddProducesSingleEntry(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true}}

	if err := registry.Add(&accounts, "playci", "com.example.app"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := registry.Add(&accounts, "playci", "com.example.app"); err != nil {
		t.Fatalf("second Add: %v", err)
	}
	pkgs := accounts[0].Packages
	if len(pkgs) != 1 || pkgs[0] != "com.example.app" {
		t.Errorf("Packages = %v, want [com.example.app] exactly once", pkgs)
	}
}

func TestAdd_unknownAccount_returnsErrUnknownAccount(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true}}

	err := registry.Add(&accounts, "ghost", "com.example.app")
	if err == nil {
		t.Fatal("Add to ghost account: expected error, got nil")
	}
	if !errors.Is(err, registry.ErrUnknownAccount) {
		t.Errorf("Add err = %v, want errors.Is(_, ErrUnknownAccount)", err)
	}
}

func TestRemove_existingPackage_dropsIt(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.a.one", "com.b.two", "com.c.three"}},
	}

	if err := registry.Remove(&accounts, "playci", "com.b.two"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got := accounts[0].Packages
	want := []string{"com.a.one", "com.c.three"}
	if !equal(got, want) {
		t.Errorf("Packages = %v, want %v", got, want)
	}
}

func TestRemove_missingPackage_isNoOp(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.a.one"}},
	}

	if err := registry.Remove(&accounts, "playci", "com.b.never"); err != nil {
		t.Errorf("Remove missing: want nil, got %v", err)
	}
	if !equal(accounts[0].Packages, []string{"com.a.one"}) {
		t.Errorf("Packages mutated by no-op Remove: %v", accounts[0].Packages)
	}
}

func TestList_returnsRegisteredPackages(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.first", "com.second"}},
		{Name: "other", Packages: []string{"com.unrelated"}},
	}

	got := registry.List(accounts, "playci")
	want := []string{"com.first", "com.second"}
	if !equal(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestList_unknownAccount_returnsNil(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true, Packages: []string{"com.x"}}}
	if got := registry.List(accounts, "ghost"); got != nil {
		t.Errorf("List(ghost) = %v, want nil", got)
	}
}

func TestRemove_doesNotCorruptCallerListSnapshot(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.a.one", "com.b.two", "com.c.three"}},
	}

	snapshot := registry.List(accounts, "playci")
	snapshotCopy := append([]string(nil), snapshot...)

	if err := registry.Remove(&accounts, "playci", "com.b.two"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if !equal(snapshot, snapshotCopy) {
		t.Errorf("List snapshot was corrupted by Remove: got %v, want %v (the snapshot taken before Remove)",
			snapshot, snapshotCopy)
	}
}

func TestList_returnsIndependentSlice(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.b.two", "com.a.one", "com.c.three"}},
	}

	got := registry.List(accounts, "playci")
	got[0] = "MUTATED"

	if accounts[0].Packages[0] != "com.b.two" {
		t.Errorf("mutating List() result mutated underlying Packages: %v", accounts[0].Packages)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
