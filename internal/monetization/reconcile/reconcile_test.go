package reconcile_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
)

var managed = []string{"listings", "taxAndComplianceSettings", "restrictedPaymentCountries"}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// TestCompute_partitionsActions asserts the plan sends a local-only product to
// create, a live-only product to delete, a differing product to patch (naming
// the changed managed fields), and an identical product to unchanged.
func TestCompute_partitionsActions(t *testing.T) {
	local := map[string]json.RawMessage{
		"newbie":  raw(`{"productId":"newbie","listings":[{"languageCode":"en-US","title":"New"}]}`),
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}]}`),
		"same":    raw(`{"productId":"same","listings":[{"languageCode":"en-US","title":"Same"}]}`),
	}
	live := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]}`),
		"same":    raw(`{"productId":"same","listings":[{"languageCode":"en-US","title":"Same"}]}`),
		"gone":    raw(`{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]}`),
	}
	plan, err := reconcile.Compute(local, live, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].ProductID != "newbie" {
		t.Errorf("Creates = %+v, want [newbie]", plan.Creates)
	}
	if len(plan.Patches) != 1 || plan.Patches[0].ProductID != "premium" {
		t.Fatalf("Patches = %+v, want [premium]", plan.Patches)
	}
	if len(plan.Patches[0].Fields) != 1 || plan.Patches[0].Fields[0] != "listings" {
		t.Errorf("Fields = %v, want [listings]", plan.Patches[0].Fields)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ProductID != "gone" {
		t.Errorf("Deletes = %+v, want [gone]", plan.Deletes)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0] != "same" {
		t.Errorf("Unchanged = %v, want [same]", plan.Unchanged)
	}
	if !plan.HasChanges() || !plan.HasDeletes() {
		t.Errorf("HasChanges/HasDeletes should both be true")
	}
}

// TestCompute_unmanagedFieldsInvisible asserts a difference outside the managed
// fields (basePlans, until slice #368) never produces a patch — the walking
// skeleton cannot clobber nesting it does not own.
func TestCompute_unmanagedFieldsInvisible(t *testing.T) {
	local := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`),
	}
	live := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]}`),
	}
	plan, err := reconcile.Compute(local, live, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("plan %+v should be empty — only basePlans differ and they are unmanaged", plan)
	}
}

// TestCompute_missingManagedFieldEqualsAbsent asserts a managed field absent on
// both sides compares equal (no phantom patch from null-vs-missing).
func TestCompute_missingManagedFieldEqualsAbsent(t *testing.T) {
	local := map[string]json.RawMessage{"p": raw(`{"productId":"p","listings":[]}`)}
	live := map[string]json.RawMessage{"p": raw(`{"productId":"p","listings":[]}`)}
	plan, err := reconcile.Compute(local, live, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("plan %+v should be empty", plan)
	}
}

// TestCompute_deterministicOrder asserts plan entries are sorted by product ID
// so dry-run output and tests are stable.
func TestCompute_deterministicOrder(t *testing.T) {
	local := map[string]json.RawMessage{
		"b": raw(`{"productId":"b"}`),
		"a": raw(`{"productId":"a"}`),
	}
	plan, err := reconcile.Compute(local, map[string]json.RawMessage{}, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.Creates[0].ProductID != "a" || plan.Creates[1].ProductID != "b" {
		t.Errorf("Creates = %+v, want sorted [a b]", plan.Creates)
	}
}
