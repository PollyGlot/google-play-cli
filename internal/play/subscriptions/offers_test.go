package subscriptions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// TestListAllOffers_wildcardPaginated asserts one wildcard walk (-/-) reads
// every offer of the app, following nextPageToken, parsing the composite key.
func TestListAllOffers_wildcardPaginated(t *testing.T) {
	rt := &subsRT{responses: []scripted{
		{200, `{"subscriptionOffers":[{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":"ACTIVE"}],"nextPageToken":"p2"}`},
		{200, `{"subscriptionOffers":[{"productId":"premium","basePlanId":"yearly","offerId":"trial","state":"DRAFT"}]}`},
	}}
	offers, err := subscriptions.ListAllOffers(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListAllOffers: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("offers = %d, want 2", len(offers))
	}
	if offers[0].ProductID != "premium" || offers[0].BasePlanID != "monthly" || offers[0].OfferID != "intro" {
		t.Errorf("offer[0] = %+v, want premium/monthly/intro", offers[0])
	}
	first := rt.requests[0]
	if !strings.Contains(first.url, "/applications/com.example.app/subscriptions/-/basePlans/-/offers?") {
		t.Errorf("url %q is not the wildcard offers.list endpoint", first.url)
	}
	if !strings.Contains(rt.requests[1].url, "pageToken=p2") {
		t.Errorf("second url %q must carry the nextPageToken", rt.requests[1].url)
	}
}

// TestListAllOffers_missingIdentity_refuses asserts an offer without its full
// composite identity is rejected at the API boundary.
func TestListAllOffers_missingIdentity_refuses(t *testing.T) {
	rt := &subsRT{responses: []scripted{
		{200, `{"subscriptionOffers":[{"productId":"premium","offerId":"intro"}]}`},
	}}
	_, err := subscriptions.ListAllOffers(context.Background(), client(rt), "com.example.app")
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("err = %v, want the missing-identity refusal", err)
	}
}

// TestCreateOffer_postsWithIDs asserts CreateOffer addresses the base plan and
// carries offerId + regionsVersion.version as query params.
func TestCreateOffer_postsWithIDs(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{"offerId":"intro"}`}}}
	body := json.RawMessage(`{"offerId":"intro","phases":[{"duration":"P1W"}]}`)
	_, err := subscriptions.CreateOffer(context.Background(), client(rt), "com.example.app", "premium", "monthly", "intro", "2022/02", body)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodPost || !strings.Contains(req.url, "/subscriptions/premium/basePlans/monthly/offers?") {
		t.Errorf("request %s %s is not offers.create", req.method, req.url)
	}
	if !strings.Contains(req.url, "offerId=intro") || !strings.Contains(req.url, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must carry offerId and regionsVersion", req.url)
	}
	if req.body != string(body) {
		t.Errorf("body %q must be the offer resource verbatim", req.body)
	}
}

// TestPatchOffer_scopedMask asserts PatchOffer addresses the offer with a
// caller-scoped updateMask.
func TestPatchOffer_scopedMask(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{}`}}}
	_, err := subscriptions.PatchOffer(context.Background(), client(rt), "com.example.app", "premium", "monthly", "intro", "2022/02", []string{"phases", "regionalConfigs"}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("PatchOffer: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodPatch || !strings.Contains(req.url, "/offers/intro?") {
		t.Errorf("request %s %s is not offers.patch", req.method, req.url)
	}
	if !strings.Contains(req.url, "updateMask=phases%2CregionalConfigs") {
		t.Errorf("url %q must scope the updateMask", req.url)
	}
}

// TestDeleteOffer_addressesOffer asserts the DELETE path.
func TestDeleteOffer_addressesOffer(t *testing.T) {
	rt := &subsRT{responses: []scripted{{204, ``}}}
	if err := subscriptions.DeleteOffer(context.Background(), client(rt), "com.example.app", "premium", "monthly", "old"); err != nil {
		t.Fatalf("DeleteOffer: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodDelete || !strings.HasSuffix(req.url, "/subscriptions/premium/basePlans/monthly/offers/old") {
		t.Errorf("request %s %s is not offers.delete", req.method, req.url)
	}
}

// TestMigrateBasePlanPrices_postsRequest asserts the migration POSTs the
// :migratePrices custom verb with the full request body (path identity echoed,
// as the schema requires) and returns the response verbatim.
func TestMigrateBasePlanPrices_postsRequest(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{}`}}}
	req := subscriptions.MigrateBasePlanPricesRequest{
		PackageName: "com.example.app",
		ProductID:   "premium",
		BasePlanID:  "monthly",
		RegionalPriceMigrations: []subscriptions.RegionalPriceMigration{
			{RegionCode: "US", OldestAllowedPriceVersionTime: "2026-01-01T00:00:00Z", PriceIncreaseType: "PRICE_INCREASE_TYPE_OPT_OUT"},
			{RegionCode: "FR", OldestAllowedPriceVersionTime: "2026-01-01T00:00:00Z"},
		},
		RegionsVersion: subscriptions.RegionsVersion{Version: "2022/02"},
	}
	_, err := subscriptions.MigrateBasePlanPrices(context.Background(), client(rt), "com.example.app", "premium", "monthly", req)
	if err != nil {
		t.Fatalf("MigrateBasePlanPrices: %v", err)
	}
	got := rt.requests[0]
	if got.method != http.MethodPost || !strings.HasSuffix(got.url, "/subscriptions/premium/basePlans/monthly:migratePrices") {
		t.Errorf("request %s %s is not basePlans.migratePrices", got.method, got.url)
	}
	for _, want := range []string{`"regionCode":"US"`, `"regionCode":"FR"`, `"oldestAllowedPriceVersionTime":"2026-01-01T00:00:00Z"`, `"version":"2022/02"`, `"priceIncreaseType":"PRICE_INCREASE_TYPE_OPT_OUT"`} {
		if !strings.Contains(got.body, want) {
			t.Errorf("body %q missing %s", got.body, want)
		}
	}
}

// TestSetStates_hitCustomVerbs asserts the state ops POST the :activate /
// :deactivate custom verbs with an empty JSON body (identity rides the path).
func TestSetStates_hitCustomVerbs(t *testing.T) {
	rt := &subsRT{responses: []scripted{{200, `{}`}, {200, `{}`}, {200, `{}`}, {200, `{}`}}}
	ctx := context.Background()
	hc := client(rt)
	if _, err := subscriptions.SetBasePlanState(ctx, hc, "com.example.app", "premium", "monthly", true); err != nil {
		t.Fatalf("activate base plan: %v", err)
	}
	if _, err := subscriptions.SetBasePlanState(ctx, hc, "com.example.app", "premium", "monthly", false); err != nil {
		t.Fatalf("deactivate base plan: %v", err)
	}
	if _, err := subscriptions.SetOfferState(ctx, hc, "com.example.app", "premium", "monthly", "intro", true); err != nil {
		t.Fatalf("activate offer: %v", err)
	}
	if _, err := subscriptions.SetOfferState(ctx, hc, "com.example.app", "premium", "monthly", "intro", false); err != nil {
		t.Fatalf("deactivate offer: %v", err)
	}
	wants := []string{
		"/subscriptions/premium/basePlans/monthly:activate",
		"/subscriptions/premium/basePlans/monthly:deactivate",
		"/subscriptions/premium/basePlans/monthly/offers/intro:activate",
		"/subscriptions/premium/basePlans/monthly/offers/intro:deactivate",
	}
	for i, want := range wants {
		if rt.requests[i].method != http.MethodPost || !strings.HasSuffix(rt.requests[i].url, want) {
			t.Errorf("request[%d] = %s %s, want POST ...%s", i, rt.requests[i].method, rt.requests[i].url, want)
		}
		if strings.TrimSpace(rt.requests[i].body) != "{}" {
			t.Errorf("request[%d] body = %q, want {}", i, rt.requests[i].body)
		}
	}
}
