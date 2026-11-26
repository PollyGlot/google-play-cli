package reconcile_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

var managed = []reconcile.Field{
	{Name: "listings"},
	{Name: "taxAndComplianceSettings"},
	{Name: "restrictedPaymentCountries"},
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

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
	plan, err := reconcile.Compute(local, live, api.Source{}, managed)
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

func TestCompute_unmanagedFieldsInvisible(t *testing.T) {
	local := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`),
	}
	live := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]}`),
	}
	plan, err := reconcile.Compute(local, live, api.Source{}, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("plan %+v should be empty: only basePlans differ and they are unmanaged", plan)
	}
}

func TestCompute_missingManagedFieldEqualsAbsent(t *testing.T) {
	local := map[string]json.RawMessage{"p": raw(`{"productId":"p","listings":[]}`)}
	live := map[string]json.RawMessage{"p": raw(`{"productId":"p","listings":[]}`)}
	plan, err := reconcile.Compute(local, live, api.Source{}, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("plan %+v should be empty", plan)
	}
}

func TestCompute_normalizerExcludesOutputOnly(t *testing.T) {
	fields := []reconcile.Field{{Name: "basePlans", Normalize: reconcile.StripKeys("state")}}
	local := map[string]json.RawMessage{
		"p": raw(`{"productId":"p","basePlans":[{"basePlanId":"monthly","regionalConfigs":[{"regionCode":"US","price":{"units":"4"}}]}]}`),
	}
	liveSame := map[string]json.RawMessage{
		"p": raw(`{"productId":"p","basePlans":[{"basePlanId":"monthly","state":"ACTIVE","regionalConfigs":[{"regionCode":"US","price":{"units":"4"}}]}]}`),
	}
	plan, err := reconcile.Compute(local, liveSame, api.Source{}, fields)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.HasChanges() {
		t.Errorf("plan %+v should be empty: only the output-only state differs", plan)
	}

	livePriceDrift := map[string]json.RawMessage{
		"p": raw(`{"productId":"p","basePlans":[{"basePlanId":"monthly","state":"ACTIVE","regionalConfigs":[{"regionCode":"US","price":{"units":"5"}}]}]}`),
	}
	plan, err = reconcile.Compute(local, livePriceDrift, api.Source{}, fields)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(plan.Patches) != 1 || plan.Patches[0].Fields[0] != "basePlans" {
		t.Errorf("plan %+v should patch basePlans: the price genuinely differs", plan)
	}
}

func TestCompute_deterministicOrder(t *testing.T) {
	local := map[string]json.RawMessage{
		"b": raw(`{"productId":"b"}`),
		"a": raw(`{"productId":"a"}`),
	}
	plan, err := reconcile.Compute(local, map[string]json.RawMessage{}, api.Source{}, managed)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if plan.Creates[0].ProductID != "a" || plan.Creates[1].ProductID != "b" {
		t.Errorf("Creates = %+v, want sorted [a b]", plan.Creates)
	}
}

func TestBasePlanDeletes_removedPlansOnly(t *testing.T) {
	local := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","basePlans":[{"basePlanId":"monthly"}]}`),
		"newbie":  raw(`{"productId":"newbie","basePlans":[{"basePlanId":"weekly"}]}`),
	}
	live := map[string]json.RawMessage{
		"premium": raw(`{"productId":"premium","basePlans":[{"basePlanId":"yearly","state":"DRAFT"},{"basePlanId":"monthly","state":"ACTIVE"},{"basePlanId":"trial","state":"DRAFT"}]}`),
		"gone":    raw(`{"productId":"gone","basePlans":[{"basePlanId":"monthly"}]}`),
	}
	got, err := reconcile.BasePlanDeletes(local, live, api.Source{})
	if err != nil {
		t.Fatalf("BasePlanDeletes: %v", err)
	}
	if len(got) != 2 || got[0].Key() != "premium/trial" || got[1].Key() != "premium/yearly" {
		t.Errorf("BasePlanDeletes = %+v, want [premium/trial premium/yearly]", got)
	}
	plan := reconcile.Plan{BasePlanDeletes: got}
	if !plan.HasDeletes() || !plan.HasChanges() {
		t.Errorf("a plan with base-plan deletes is destructive and non-empty; got HasDeletes=%v HasChanges=%v", plan.HasDeletes(), plan.HasChanges())
	}
}

func TestBasePlanDeletes_unaddressablePlan_refuses(t *testing.T) {
	local := map[string]json.RawMessage{"premium": raw(`{"productId":"premium"}`)}
	live := map[string]json.RawMessage{"premium": raw(`{"productId":"premium","basePlans":[{"state":"DRAFT"}]}`)}
	if _, err := reconcile.BasePlanDeletes(local, live, api.Source{}); err == nil {
		t.Fatal("want an error on a base plan without basePlanId")
	}
}

func TestComputeChildren_typedAndInDisplayOrder(t *testing.T) {
	offer := raw(`{"offerTags":[]}`)
	local := map[reconcile.Key]json.RawMessage{
		{ProductID: "a", ParentID: "p", OfferID: "x"}:   offer,
		{ProductID: "a-b", ParentID: "p", OfferID: "x"}: offer,
	}
	live := map[reconcile.Key]json.RawMessage{
		{ProductID: "a", ParentID: "p", OfferID: "gone"}: offer,
	}
	creates, patches, deletes, err := reconcile.ComputeChildren(local, live, api.Source{}, []reconcile.Field{{Name: "offerTags"}})
	if err != nil {
		t.Fatalf("ComputeChildren: %v", err)
	}
	if len(creates) != 2 || creates[0].ProductID != "a-b" || creates[1].ProductID != "a" {
		t.Fatalf("creates = %+v, want a-b/p/x then a/p/x", creates)
	}
	if c := creates[1]; c.ParentID != "p" || c.OfferID != "x" || c.Key() != "a/p/x" {
		t.Errorf("create identity = %+v, want typed a / p / x", c)
	}
	if len(patches) != 0 || len(deletes) != 1 || deletes[0].Key() != "a/p/gone" {
		t.Errorf("patches = %+v, deletes = %+v, want none and a/p/gone", patches, deletes)
	}
}

func TestPlanEntries_orderAndOps(t *testing.T) {
	p := reconcile.Plan{
		Creates:         []reconcile.Change{{ProductID: "c"}},
		Patches:         []reconcile.Change{{ProductID: "p", Fields: []string{"listings"}}},
		Deletes:         []reconcile.Change{{ProductID: "d"}},
		BasePlanDeletes: []reconcile.Change{{ProductID: "p", ParentID: "old"}},
		OfferCreates:    []reconcile.Change{{ProductID: "p", ParentID: "bp", OfferID: "new"}},
		OfferPatches:    []reconcile.Change{{ProductID: "p", ParentID: "bp", OfferID: "chg"}},
		OfferDeletes:    []reconcile.Change{{ProductID: "p", ParentID: "bp", OfferID: "del"}},
		StateChanges: []reconcile.StateChange{
			{Kind: reconcile.KindBasePlan, ProductID: "p", BasePlanID: "bp", From: "DRAFT", To: "ACTIVE"},
			{Kind: reconcile.KindOffer, ProductID: "p", PurchaseOptionID: "po", OfferID: "o", From: "ACTIVE", To: "INACTIVE"},
			{Kind: reconcile.KindOffer, ProductID: "p", PurchaseOptionID: "po", OfferID: "pre", From: "ACTIVE", To: "CANCELLED"},
		},
	}
	var got []string
	for _, e := range p.Entries() {
		got = append(got, e.Op+" "+e.Kind+" "+e.Target())
	}
	want := []string{
		"create  c",
		"patch  p",
		"create offer p/bp/new",
		"patch offer p/bp/chg",
		"activate basePlan p/bp",
		"deactivate offer p/po/o",
		"cancel offer p/po/pre",
		"delete offer p/bp/del",
		"delete basePlan p/old",
		"delete  d",
	}
	if len(got) != len(want) {
		t.Fatalf("Entries = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Entries[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
