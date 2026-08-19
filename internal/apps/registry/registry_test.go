package registry_test

import (
	"errors"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// TestAdd_Has_findsAddedPackage is the tracer bullet: registry.Add records
// the package under the named account, registry.Has reads it back.
func TestAdd_Has_findsAddedPackage(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true}}

	if err := registry.Add(&accounts, "playci", "com.example.app"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !registry.Has(accounts, "playci", "com.example.app") {
		t.Error("Has after Add: want true")
	}
}

// TestAdd_idempotent_doubleAddProducesSingleEntry asserts that adding the
// same (account, pkg) pair twice does not double the Packages slice. Real
// users will re-run `gplay apps add` to confirm an existing registration;
// idempotence keeps that flow safe.
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

// TestAdd_unknownAccount_returnsErrUnknownAccount keeps the registry
// honest: every entry must be attached to a known Account, so the
// caller surfaces a typed CLI-misuse exit (2) rather than silently
// creating a phantom account record.
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

// TestRemove_existingPackage_dropsIt removes one entry and leaves
// neighbours intact.
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

// TestRemove_missingPackage_isNoOp confirms Remove on a package that was
// never registered is a no-op rather than an error. The intent is "make
// sure this is not in the registry": calling Remove twice should not
// fail.
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

// TestList_returnsRegisteredPackages returns the package set under an
// account. Iteration order matches insertion (the slice order on disk);
// the registry does not sort.
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

// TestList_unknownAccount_returnsNil documents the read-side contract:
// an unknown account is observable as "no packages" without an error,
// which lets `gplay apps list --account foo` print an empty result
// rather than failing.
func TestList_unknownAccount_returnsNil(t *testing.T) {
	accounts := []config.Account{{Name: "playci", Active: true, Packages: []string{"com.x"}}}
	if got := registry.List(accounts, "ghost"); got != nil {
		t.Errorf("List(ghost) = %v, want nil", got)
	}
}

// TestRemove_doesNotCorruptCallerListSnapshot asserts the slice-aliasing
// guard: a caller that holds a slice returned by List MUST NOT see its
// contents silently rewritten by a subsequent Remove. The pre-fix
// implementation used `append(pkgs[:j], pkgs[j+1:]...)` which mutates
// the underlying array; the caller's snapshot saw the second-to-last
// element duplicated. This test pins the contract that List returns
// an independent view, and Remove does not stomp on it.
func TestRemove_doesNotCorruptCallerListSnapshot(t *testing.T) {
	accounts := []config.Account{
		{Name: "playci", Active: true, Packages: []string{"com.a.one", "com.b.two", "com.c.three"}},
	}

	snapshot := registry.List(accounts, "playci")
	snapshotCopy := append([]string(nil), snapshot...) // expected value snapshot took

	if err := registry.Remove(&accounts, "playci", "com.b.two"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if !equal(snapshot, snapshotCopy) {
		t.Errorf("List snapshot was corrupted by Remove: got %v, want %v (the snapshot taken before Remove)",
			snapshot, snapshotCopy)
	}
}

// TestList_returnsIndependentSlice asserts the caller can sort/filter
// the returned slice without corrupting the registry. Pre-fix List
// returned the underlying storage; a single sort.Strings would silently
// destroy insertion order on disk.
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
