package apply_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// renderPlan exercises every entry the subscription plan can carry: a create, a
// patch, a subscription delete, a base-plan delete, the three offer actions,
// and both state verbs on base plans and offers.
func renderPlan() reconcile.Plan {
	return reconcile.Plan{
		Creates:         []reconcile.Change{{ProductID: "premium"}},
		Patches:         []reconcile.Change{{ProductID: "basic", Fields: []string{"listings", "basePlans"}}},
		Deletes:         []reconcile.Change{{ProductID: "legacy"}},
		BasePlanDeletes: []reconcile.Change{{ProductID: "basic", ParentID: "old-monthly"}},
		OfferCreates:    []reconcile.Change{{ProductID: "premium", ParentID: "monthly", OfferID: "intro"}},
		OfferPatches:    []reconcile.Change{{ProductID: "basic", ParentID: "yearly", OfferID: "winback", Fields: []string{"phases"}}},
		OfferDeletes:    []reconcile.Change{{ProductID: "basic", ParentID: "yearly", OfferID: "stale"}},
		StateChanges: []reconcile.StateChange{
			{Kind: "basePlan", ProductID: "premium", BasePlanID: "monthly", From: "DRAFT", To: "ACTIVE"},
			{Kind: "offer", ProductID: "premium", BasePlanID: "monthly", OfferID: "intro", From: "DRAFT", To: "ACTIVE"},
			{Kind: "basePlan", ProductID: "basic", BasePlanID: "yearly", From: "ACTIVE", To: "INACTIVE"},
			{Kind: "offer", ProductID: "basic", BasePlanID: "yearly", OfferID: "winback", From: "ACTIVE", To: "INACTIVE"},
		},
		Unchanged: []string{"gold"},
	}
}

// TestRender_golden pins the table, markdown and JSON views of the plan byte
// for byte, planned and executed, so moving the renderer (ARCH-07, #591)
// provably changes nothing a user or a jq gate sees.
func TestRender_golden(t *testing.T) {
	cases := []struct {
		name string
		p    apply.Payload
	}{
		{"dryrun", apply.Payload{Package: "com.example.app", Plan: renderPlan(), DryRun: true, Requires: []string{"confirm"}}},
		{"applied", apply.Payload{Package: "com.example.app", Plan: renderPlan()}},
		{"nochange", apply.Payload{Package: "com.example.app", Plan: reconcile.Plan{Unchanged: []string{"gold"}}}},
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
