package iap_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/iap"
)

type iapRT struct {
	mu        sync.Mutex
	requests  []recordedReq
	responses []scripted
}

type recordedReq struct {
	method string
	url    string
	body   string
}

type scripted struct {
	status int
	body   string
}

func (r *iapRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	r.requests = append(r.requests, recordedReq{method: req.Method, url: req.URL.String(), body: body})
	if len(r.responses) == 0 {
		return jsonResp(200, `{}`), nil
	}
	next := r.responses[0]
	r.responses = r.responses[1:]
	return jsonResp(next.status, next.body), nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

// TestListOneTimeProducts_paginates asserts the v2 list follows nextPageToken
// and parses productId per item.
func TestListOneTimeProducts_paginates(t *testing.T) {
	rt := &iapRT{responses: []scripted{
		{200, `{"oneTimeProducts":[{"productId":"coins100","purchaseOptions":[{"purchaseOptionId":"buy","state":"ACTIVE"}]}],"nextPageToken":"p2"}`},
		{200, `{"oneTimeProducts":[{"productId":"gems50"}]}`},
	}}
	items, err := iap.ListOneTimeProducts(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListOneTimeProducts: %v", err)
	}
	if len(items) != 2 || items[0].ProductID != "coins100" || items[1].ProductID != "gems50" {
		t.Fatalf("items = %+v, want coins100 then gems50", items)
	}
	if !strings.Contains(rt.requests[0].url, "/applications/com.example.app/oneTimeProducts?") {
		t.Errorf("url %q is not onetimeproducts.list", rt.requests[0].url)
	}
	if !strings.Contains(rt.requests[1].url, "pageToken=p2") {
		t.Errorf("second url %q must carry the token", rt.requests[1].url)
	}
}

// TestListAllOffers_wildcard asserts one -/- walk reads every OTP offer with
// its full composite identity.
func TestListAllOffers_wildcard(t *testing.T) {
	rt := &iapRT{responses: []scripted{
		{200, `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":"ACTIVE"}]}`},
	}}
	offers, err := iap.ListAllOffers(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListAllOffers: %v", err)
	}
	if len(offers) != 1 || offers[0].PurchaseOptionID != "buy" || offers[0].OfferID != "promo" {
		t.Fatalf("offers = %+v, want coins100/buy/promo", offers)
	}
	if !strings.Contains(rt.requests[0].url, "/oneTimeProducts/-/purchaseOptions/-/offers?") {
		t.Errorf("url %q is not the wildcard offers.list", rt.requests[0].url)
	}
}

// TestPatchOneTimeProduct_upsert asserts patch carries allowMissing for a
// create (no updateMask) and a scoped updateMask for an edit.
func TestPatchOneTimeProduct_upsert(t *testing.T) {
	rt := &iapRT{responses: []scripted{{200, `{}`}, {200, `{}`}}}
	body := json.RawMessage(`{"productId":"coins100","listings":[]}`)
	if _, err := iap.PatchOneTimeProduct(context.Background(), client(rt), "com.example.app", "coins100", "2022/02", nil, true, body); err != nil {
		t.Fatalf("Patch(create): %v", err)
	}
	if _, err := iap.PatchOneTimeProduct(context.Background(), client(rt), "com.example.app", "coins100", "2022/02", []string{"listings"}, false, body); err != nil {
		t.Fatalf("Patch(edit): %v", err)
	}
	create, edit := rt.requests[0], rt.requests[1]
	if create.method != http.MethodPatch || !strings.Contains(create.url, "/onetimeproducts/coins100?") {
		t.Errorf("create request %s %s is not onetimeproducts.patch", create.method, create.url)
	}
	if !strings.Contains(create.url, "allowMissing=true") || strings.Contains(create.url, "updateMask") {
		t.Errorf("create url %q must upsert without a mask", create.url)
	}
	if !strings.Contains(edit.url, "updateMask=listings") || strings.Contains(edit.url, "allowMissing") {
		t.Errorf("edit url %q must send a scoped mask without allowMissing", edit.url)
	}
	if !strings.Contains(create.url, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must pin the regions version", create.url)
	}
}

// TestDeleteOneTimeProduct_addresses asserts the DELETE path.
func TestDeleteOneTimeProduct_addresses(t *testing.T) {
	rt := &iapRT{responses: []scripted{{204, ``}}}
	if err := iap.DeleteOneTimeProduct(context.Background(), client(rt), "com.example.app", "gems50"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodDelete || !strings.HasSuffix(req.url, "/oneTimeProducts/gems50") {
		t.Errorf("request %s %s is not onetimeproducts.delete", req.method, req.url)
	}
}

// TestBatchUpdateOffers_groupsPerPurchaseOption asserts the batch update POSTs
// one request per offer with allowMissing/updateMask per entry.
func TestBatchUpdateOffers_groupsPerPurchaseOption(t *testing.T) {
	rt := &iapRT{responses: []scripted{{200, `{}`}}}
	err := iap.BatchUpdateOffers(context.Background(), client(rt), "com.example.app", "coins100", "buy", []iap.OfferUpdate{
		{Offer: json.RawMessage(`{"offerId":"promo"}`), AllowMissing: true},
		{Offer: json.RawMessage(`{"offerId":"sale"}`), UpdateMask: []string{"regionalPricingAndAvailabilityConfigs"}},
	}, "2022/02")
	if err != nil {
		t.Fatalf("BatchUpdateOffers: %v", err)
	}
	req := rt.requests[0]
	if req.method != http.MethodPost || !strings.HasSuffix(strings.Split(req.url, "?")[0], "/oneTimeProducts/coins100/purchaseOptions/buy/offers:batchUpdate") {
		t.Errorf("request %s %s is not offers.batchUpdate", req.method, req.url)
	}
	for _, want := range []string{`"allowMissing":true`, `"updateMask":"regionalPricingAndAvailabilityConfigs"`, `"offerId":"promo"`, `"version":"2022/02"`} {
		if !strings.Contains(req.body, want) {
			t.Errorf("body %q missing %s", req.body, want)
		}
	}
}

// TestBatchDeleteOffers_postsIdentities asserts the batch delete names each
// offer's full identity.
func TestBatchDeleteOffers_postsIdentities(t *testing.T) {
	rt := &iapRT{responses: []scripted{{200, `{}`}}}
	err := iap.BatchDeleteOffers(context.Background(), client(rt), "com.example.app", "coins100", "buy", []string{"promo"})
	if err != nil {
		t.Fatalf("BatchDeleteOffers: %v", err)
	}
	req := rt.requests[0]
	if !strings.HasSuffix(req.url, "/offers:batchDelete") {
		t.Errorf("url %q is not offers.batchDelete", req.url)
	}
	for _, want := range []string{`"offerId":"promo"`, `"productId":"coins100"`, `"purchaseOptionId":"buy"`, `"packageName":"com.example.app"`} {
		if !strings.Contains(req.body, want) {
			t.Errorf("body %q missing %s", req.body, want)
		}
	}
}

// TestListInAppProducts_legacyTokenPaging asserts the legacy list follows the
// tokenPagination envelope and keys items by sku.
func TestListInAppProducts_legacyTokenPaging(t *testing.T) {
	rt := &iapRT{responses: []scripted{
		{200, `{"inappproduct":[{"sku":"legacy_coins","purchaseType":"managedUser","status":"active"}],"tokenPagination":{"nextPageToken":"t2"}}`},
		{200, `{"inappproduct":[{"sku":"legacy_gems"}]}`},
	}}
	items, err := iap.ListInAppProducts(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("ListInAppProducts: %v", err)
	}
	if len(items) != 2 || items[0].SKU != "legacy_coins" || items[1].SKU != "legacy_gems" {
		t.Fatalf("items = %+v, want legacy_coins then legacy_gems", items)
	}
	if !strings.Contains(rt.requests[0].url, "/applications/com.example.app/inappproducts") {
		t.Errorf("url %q is not inappproducts.list", rt.requests[0].url)
	}
	if !strings.Contains(rt.requests[1].url, "token=t2") {
		t.Errorf("second url %q must carry the legacy token param", rt.requests[1].url)
	}
}
