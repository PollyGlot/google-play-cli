package apply_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/iap/apply"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// TestRenderJSON_dryRunDestructive_golden freezes the one-time-product plan
// with every op a CI gate can meet: a legacy promotion rendered as "migrate"
// (and discounted from the create count), the three state verbs including the
// irreversible pre-order cancel, and both gates in "requires".
func TestRenderJSON_dryRunDestructive_golden(t *testing.T) {
	plan := reconcile.Plan{
		Creates:      []reconcile.Change{{ProductID: "coins_100"}, {ProductID: "remove_ads"}},
		Patches:      []reconcile.Change{{ProductID: "coins_500", Fields: []string{"listings", "purchaseOptions"}}},
		OfferCreates: []reconcile.Change{{ProductID: "coins_500", ParentID: "buy", OfferID: "launch-discount"}},
		OfferPatches: []reconcile.Change{{ProductID: "coins_500", ParentID: "buy", OfferID: "weekend-sale", Fields: []string{"regionalPricingAndAvailabilityConfigs"}}},
		OfferDeletes: []reconcile.Change{{ProductID: "coins_500", ParentID: "buy", OfferID: "expired-promo"}},
		Deletes:      []reconcile.Change{{ProductID: "coins_legacy"}},
		StateChanges: []reconcile.StateChange{
			{Kind: "purchaseOption", ProductID: "coins_100", PurchaseOptionID: "buy", From: "DRAFT", To: "ACTIVE"},
			{Kind: "offer", ProductID: "coins_500", PurchaseOptionID: "buy", OfferID: "weekend-sale", From: "ACTIVE", To: "INACTIVE"},
			{Kind: "offer", ProductID: "season_pass", PurchaseOptionID: "preorder", OfferID: "early-bird", From: "ACTIVE", To: "CANCELLED"},
		},
		Unchanged: []string{"coins_1000"},
	}
	p := apply.Payload{
		Package:   "com.example.app",
		Dir:       "./catalog/iap",
		Plan:      plan,
		Migrating: map[string]bool{"remove_ads": true},
		DryRun:    true,
		Requires:  []string{"confirm", "migrate"},
	}
	outputtest.GoldenJSON(t, "dry_run_destructive.json.golden", p)
}

// TestRenderJSON_applied_golden freezes the executed plan: dryRun:false, no
// requires key, and a nil Migrating set rendering a zero migrate count.
func TestRenderJSON_applied_golden(t *testing.T) {
	plan := reconcile.Plan{
		Patches:   []reconcile.Change{{ProductID: "coins_500", Fields: []string{"listings"}}},
		Unchanged: []string{"coins_100"},
	}
	outputtest.GoldenJSON(t, "applied.json.golden", apply.Payload{Package: "com.example.app", Dir: "./catalog/iap", Plan: plan})
}

// TestRenderJSON_noChanges_golden pins an in-sync catalog to "changes": []
// (never null), the shape a drift check greps for.
func TestRenderJSON_noChanges_golden(t *testing.T) {
	p := apply.Payload{Package: "com.example.app", Dir: "./catalog/iap", Plan: reconcile.Plan{Unchanged: []string{"coins_100"}}, DryRun: true}
	outputtest.GoldenJSON(t, "no_changes.json.golden", p)
}
