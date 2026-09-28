package planview_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/monetization/planview"
	"github.com/PollyGlot/google-play-cli/internal/monetization/reconcile"
)

// TestAxis_namesTheMiddleLevel asserts what the axis decides for one and the
// same plan: the JSON key of the middle level, the markdown heading and the
// catalog's own summary counter. The full views are pinned byte for byte by
// the goldens of commands/iap/apply and commands/subscriptions/apply.
func TestAxis_namesTheMiddleLevel(t *testing.T) {
	plan := reconcile.Plan{OfferCreates: []reconcile.Change{{ProductID: "p", ParentID: "mid", OfferID: "o"}}}
	cases := []struct {
		axis                   planview.Axis
		key, heading, counter  string
		otherKey, otherCounter string
	}{
		{planview.Subscriptions, `"basePlanId": "mid"`, "## subscriptions apply: pkg", "basePlanDelete=0", "purchaseOptionId", "migrate="},
		{planview.OneTimeProducts, `"purchaseOptionId": "mid"`, "## iap apply: pkg", "migrate=0", "basePlanId", "basePlanDelete="},
	}
	for _, tc := range cases {
		v := planview.View{Axis: tc.axis, Package: "pkg", Plan: plan, DryRun: true}
		var js, md bytes.Buffer
		if err := v.JSON(&js); err != nil {
			t.Fatal(err)
		}
		if err := v.Human(&md, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(js.String(), tc.key) || strings.Contains(js.String(), tc.otherKey) {
			t.Errorf("JSON = %s, want %s and no %s", js.String(), tc.key, tc.otherKey)
		}
		if !strings.HasPrefix(md.String(), tc.heading) {
			t.Errorf("markdown = %q, want heading %q", md.String(), tc.heading)
		}
		if !strings.Contains(md.String(), "  create offer p/mid/o\n") {
			t.Errorf("markdown = %q, want the offer create line", md.String())
		}
		if !strings.Contains(md.String(), tc.counter) || strings.Contains(md.String(), tc.otherCounter) {
			t.Errorf("summary = %q, want %s and no %s", md.String(), tc.counter, tc.otherCounter)
		}
	}
}
