package apply_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/iap/apply"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// renderPlan exercises every entry the one-time-product plan can carry: a create
// and a legacy→v2 migrate, a patch, a product delete, the three offer actions,
// and every state verb (activate, deactivate, cancel) on both kinds.
func renderPlan() reconcile.Plan {
	return reconcile.Plan{
		Creates:      []reconcile.Change{{ProductID: "coins"}, {ProductID: "legacy_gem"}},
		Patches:      []reconcile.Change{{ProductID: "sword", Fields: []string{"listings", "purchaseOptions"}}},
		Deletes:      []reconcile.Change{{ProductID: "old"}},
		OfferCreates: []reconcile.Change{{ProductID: "coins", ParentID: "buy", OfferID: "launch"}},
		OfferPatches: []reconcile.Change{{ProductID: "sword", ParentID: "buy", OfferID: "promo", Fields: []string{"offerTags"}}},
		OfferDeletes: []reconcile.Change{{ProductID: "sword", ParentID: "buy", OfferID: "stale"}},
		StateChanges: []reconcile.StateChange{
			{Kind: "purchaseOption", ProductID: "coins", PurchaseOptionID: "buy", From: "DRAFT", To: "ACTIVE"},
			{Kind: "offer", ProductID: "coins", PurchaseOptionID: "buy", OfferID: "launch", From: "DRAFT", To: "ACTIVE"},
			{Kind: "offer", ProductID: "sword", PurchaseOptionID: "buy", OfferID: "promo", From: "ACTIVE", To: "INACTIVE"},
			{Kind: "offer", ProductID: "sword", PurchaseOptionID: "preorder", OfferID: "early", From: "ACTIVE", To: "CANCELLED"},
			{Kind: "purchaseOption", ProductID: "sword", PurchaseOptionID: "rent", From: "ACTIVE", To: "INACTIVE"},
		},
		Unchanged: []string{"shield"},
	}
}

// TestRender_golden pins the table, markdown and JSON views of the plan byte
// for byte, planned and executed, so moving the renderer (ARCH-07, #591)
// provably changes nothing a user or a jq gate sees.
func TestRender_golden(t *testing.T) {
	migrating := map[string]bool{"legacy_gem": true}
	cases := []struct {
		name string
		p    apply.Payload
	}{
		{"dryrun", apply.Payload{Package: "com.example.app", Plan: renderPlan(), Migrating: migrating, DryRun: true, Requires: []string{"confirm", "migrate"}}},
		{"applied", apply.Payload{Package: "com.example.app", Plan: renderPlan(), Migrating: migrating}},
		{"nochange", apply.Payload{Package: "com.example.app", Plan: reconcile.Plan{Unchanged: []string{"shield"}}}},
	}
	for _, tc := range cases {
		r := tc.p.Renderers()
		for _, v := range []struct {
			ext    string
			render func(io.Writer) error
		}{{"table.txt", r.Table}, {"md", r.Markdown}, {"json", r.JSON}} {
			var buf bytes.Buffer
			if err := v.render(&buf); err != nil {
				t.Fatalf("%s.%s: %v", tc.name, v.ext, err)
			}
			testkit.Golden(t, "render/"+tc.name+"."+v.ext, buf.Bytes())
		}
	}
}
