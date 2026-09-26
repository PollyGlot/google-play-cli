package apply_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// fullPlan touches every plan slice, so the golden pins the flat change order
// (creates, patches, offers, states, then deletes innermost-first) and how the
// typed identities render as productId/basePlanId/offerId.
func fullPlan() reconcile.Plan {
	return reconcile.Plan{
		Creates:         []reconcile.Change{{ProductID: "premium_yearly"}},
		Patches:         []reconcile.Change{{ProductID: "premium_monthly", Fields: []string{"basePlans", "listings"}}},
		OfferCreates:    []reconcile.Change{{ProductID: "premium_monthly", ParentID: "monthly-autorenew", OfferID: "intro-7d"}},
		OfferPatches:    []reconcile.Change{{ProductID: "premium_monthly", ParentID: "monthly-autorenew", OfferID: "winback", Fields: []string{"phases"}}},
		OfferDeletes:    []reconcile.Change{{ProductID: "premium_monthly", ParentID: "monthly-autorenew", OfferID: "legacy-promo"}},
		BasePlanDeletes: []reconcile.Change{{ProductID: "premium_monthly", ParentID: "monthly-prepaid"}},
		Deletes:         []reconcile.Change{{ProductID: "pro_legacy"}},
		StateChanges: []reconcile.StateChange{
			{Kind: "basePlan", ProductID: "premium_yearly", BasePlanID: "yearly-autorenew", From: "DRAFT", To: "ACTIVE"},
			{Kind: "offer", ProductID: "premium_monthly", BasePlanID: "monthly-autorenew", OfferID: "winback", From: "ACTIVE", To: "INACTIVE"},
		},
		Unchanged: []string{"basic_monthly"},
	}
}

// TestRenderJSON_dryRunDestructive_golden freezes the plan a CI gate reads:
// every op kind, the summary counts and the "requires" gate a destructive plan
// surfaces before --confirm.
func TestRenderJSON_dryRunDestructive_golden(t *testing.T) {
	p := apply.Payload{Package: "com.example.app", Dir: "./catalog/subscriptions", Plan: fullPlan(), DryRun: true, Requires: []string{"confirm"}}
	outputtest.GoldenJSON(t, "dry_run_destructive.json.golden", p)
}

// TestRenderJSON_applied_golden freezes the executed plan: same flat schema,
// dryRun:false and no requires key once the gate was passed.
func TestRenderJSON_applied_golden(t *testing.T) {
	plan := reconcile.Plan{
		Patches:   []reconcile.Change{{ProductID: "premium_monthly", Fields: []string{"listings"}}},
		Unchanged: []string{"basic_monthly", "premium_yearly"},
	}
	p := apply.Payload{Package: "com.example.app", Dir: "./catalog/subscriptions", Plan: plan}
	outputtest.GoldenJSON(t, "applied.json.golden", p)
}

// TestRenderJSON_noChanges_golden pins an in-sync catalog to "changes": []
// (never null) with an all-zero summary but the unchanged count.
func TestRenderJSON_noChanges_golden(t *testing.T) {
	p := apply.Payload{Package: "com.example.app", Dir: "./catalog/subscriptions", Plan: reconcile.Plan{Unchanged: []string{"premium_monthly"}}, DryRun: true}
	outputtest.GoldenJSON(t, "no_changes.json.golden", p)
}
