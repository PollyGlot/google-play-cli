package iap_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

type scripted struct {
	status int
	body   string
}

// iapFake serves the scripted responses in call order, then 200 {} once they
// run out.
func iapFake(responses []scripted) *testkit.Fake {
	var (
		mu sync.Mutex
		i  int
	)
	return testkit.NewFake(func(testkit.Call) (int, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(responses) {
			return http.StatusOK, `{}`, true
		}
		next := responses[i]
		i++
		return next.status, next.body, true
	})
}

func client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

// TestListOneTimeProducts_paginates asserts the v2 list follows nextPageToken
// and parses productId per item.
func TestListOneTimeProducts_paginates(t *testing.T) {
	rt := iapFake([]scripted{
		{200, `{"oneTimeProducts":[{"productId":"coins100","purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE"}]}],"nextPageToken":"p2"}`},
		{200, `{"oneTimeProducts":[{"productId":"gems50"}]}`},
	})
	items, err := iap.ListOneTimeProducts(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListOneTimeProducts: %v", err)
	}
	if len(items) != 2 || items[0].ProductID != "coins100" || items[1].ProductID != "gems50" {
		t.Fatalf("items = %+v, want coins100 then gems50", items)
	}
	if !strings.Contains(rt.Calls()[0].URL, "/applications/com.example.app/oneTimeProducts?") {
		t.Errorf("url %q is not onetimeproducts.list", rt.Calls()[0].URL)
	}
	if !strings.Contains(rt.Calls()[1].URL, "pageToken=p2") {
		t.Errorf("second url %q must carry the token", rt.Calls()[1].URL)
	}
}

// TestListAllOffers_wildcard asserts one -/- walk reads every OTP offer with
// its full composite identity.
func TestListAllOffers_wildcard(t *testing.T) {
	rt := iapFake([]scripted{
		{200, `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE"}]}`},
	})
	offers, err := iap.ListAllOffers(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListAllOffers: %v", err)
	}
	if len(offers) != 1 || offers[0].PurchaseOptionID != "buy" || offers[0].OfferID != "promo" {
		t.Fatalf("offers = %+v, want coins100/buy/promo", offers)
	}
	if !strings.Contains(rt.Calls()[0].URL, "/oneTimeProducts/-/purchaseOptions/-/offers?") {
		t.Errorf("url %q is not the wildcard offers.list", rt.Calls()[0].URL)
	}
}

// TestPatchOneTimeProduct_upsert asserts patch carries allowMissing for a
// create (no updateMask) and a scoped updateMask for an edit.
func TestPatchOneTimeProduct_upsert(t *testing.T) {
	rt := iapFake([]scripted{{200, `{}`}, {200, `{}`}})
	body := json.RawMessage(`{"productId":"coins100","listings":[]}`)
	if _, err := iap.PatchOneTimeProduct(context.Background(), client(rt), "com.example.app", "coins100", "2022/02", nil, true, body); err != nil {
		t.Fatalf("Patch(create): %v", err)
	}
	if _, err := iap.PatchOneTimeProduct(context.Background(), client(rt), "com.example.app", "coins100", "2022/02", []string{"listings"}, false, body); err != nil {
		t.Fatalf("Patch(edit): %v", err)
	}
	create, edit := rt.Calls()[0], rt.Calls()[1]
	if create.Method != http.MethodPatch || !strings.Contains(create.URL, "/onetimeproducts/coins100?") {
		t.Errorf("create request %s %s is not onetimeproducts.patch", create.Method, create.URL)
	}
	if !strings.Contains(create.URL, "allowMissing=true") || strings.Contains(create.URL, "updateMask") {
		t.Errorf("create url %q must upsert without a mask", create.URL)
	}
	if !strings.Contains(edit.URL, "updateMask=listings") || strings.Contains(edit.URL, "allowMissing") {
		t.Errorf("edit url %q must send a scoped mask without allowMissing", edit.URL)
	}
	if !strings.Contains(create.URL, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must pin the regions version", create.URL)
	}
}

// TestDeleteOneTimeProduct_addresses asserts the DELETE path.
func TestDeleteOneTimeProduct_addresses(t *testing.T) {
	rt := iapFake([]scripted{{204, ``}})
	if err := iap.DeleteOneTimeProduct(context.Background(), client(rt), "com.example.app", "gems50"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodDelete || !strings.HasSuffix(req.URL, "/oneTimeProducts/gems50") {
		t.Errorf("request %s %s is not onetimeproducts.delete", req.Method, req.URL)
	}
}

// TestBatchUpdateOffers_groupsPerPurchaseOption asserts the batch update POSTs
// one request per offer with allowMissing/updateMask per entry.
func TestBatchUpdateOffers_groupsPerPurchaseOption(t *testing.T) {
	rt := iapFake([]scripted{{200, `{}`}})
	err := iap.BatchUpdateOffers(context.Background(), client(rt), "com.example.app", "coins100", "buy", []iap.OfferUpdate{
		{Offer: json.RawMessage(`{"offerId":"promo"}`), AllowMissing: true},
		{Offer: json.RawMessage(`{"offerId":"sale"}`), UpdateMask: []string{"regionalPricingAndAvailabilityConfigs"}},
	}, "2022/02")
	if err != nil {
		t.Fatalf("BatchUpdateOffers: %v", err)
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodPost || !strings.HasSuffix(strings.Split(req.URL, "?")[0], "/oneTimeProducts/coins100/purchaseOptions/buy/offers:batchUpdate") {
		t.Errorf("request %s %s is not offers.batchUpdate", req.Method, req.URL)
	}
	for _, want := range []string{`"allowMissing":true`, `"updateMask":"regionalPricingAndAvailabilityConfigs"`, `"offerId":"promo"`, `"version":"2022/02"`} {
		if !strings.Contains(string(req.Body), want) {
			t.Errorf("body %q missing %s", string(req.Body), want)
		}
	}
}

// TestBatchDeleteOffers_postsIdentities asserts the batch delete names each
// offer's full identity.
func TestBatchDeleteOffers_postsIdentities(t *testing.T) {
	rt := iapFake([]scripted{{200, `{}`}})
	err := iap.BatchDeleteOffers(context.Background(), client(rt), "com.example.app", "coins100", "buy", []string{"promo"})
	if err != nil {
		t.Fatalf("BatchDeleteOffers: %v", err)
	}
	req := rt.Calls()[0]
	if !strings.HasSuffix(req.URL, "/offers:batchDelete") {
		t.Errorf("url %q is not offers.batchDelete", req.URL)
	}
	for _, want := range []string{`"offerId":"promo"`, `"productId":"coins100"`, `"purchaseOptionId":"buy"`, `"packageName":"com.example.app"`} {
		if !strings.Contains(string(req.Body), want) {
			t.Errorf("body %q missing %s", string(req.Body), want)
		}
	}
}

// TestListInAppProducts_legacyTokenPaging asserts the legacy list follows the
// tokenPagination envelope and keys items by sku.
func TestListInAppProducts_legacyTokenPaging(t *testing.T) {
	rt := iapFake([]scripted{
		{200, `{"inappproduct":[{"sku":"legacy_coins","purchaseType":"managedUser","status":"active"}],"tokenPagination":{"nextPageToken":"t2"}}`},
		{200, `{"inappproduct":[{"sku":"legacy_gems"}]}`},
	})
	items, err := iap.ListInAppProducts(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListInAppProducts: %v", err)
	}
	if len(items) != 2 || items[0].SKU != "legacy_coins" || items[1].SKU != "legacy_gems" {
		t.Fatalf("items = %+v, want legacy_coins then legacy_gems", items)
	}
	if !strings.Contains(rt.Calls()[0].URL, "/applications/com.example.app/inappproducts") {
		t.Errorf("url %q is not inappproducts.list", rt.Calls()[0].URL)
	}
	if !strings.Contains(rt.Calls()[1].URL, "token=t2") {
		t.Errorf("second url %q must carry the legacy token param", rt.Calls()[1].URL)
	}
}
