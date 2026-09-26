package subscriptions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

type scripted struct {
	status int
	body   string
}

// subsFake serves the scripted responses in call order, then 200 {} once they
// run out, and records every request for assertions. No network.
func subsFake(responses []scripted) *testkit.Fake {
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

// TestList_paginatesToCompletion asserts List follows nextPageToken across
// pages, parses each subscription's productId, and keeps the verbatim resource
// bytes for the pass-through.
func TestList_paginatesToCompletion(t *testing.T) {
	rt := subsFake([]scripted{
		{200, `{"subscriptions":[{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}],"nextPageToken":"p2"}`},
		{200, `{"subscriptions":[{"productId":"pro","listings":[{"languageCode":"en-US","title":"Pro"}]}]}`},
	})
	items, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ProductID != "premium" || items[1].ProductID != "pro" {
		t.Fatalf("items = %+v, want premium then pro", items)
	}
	if !strings.Contains(string(items[0].Raw), `"title":"Premium"`) {
		t.Errorf("Raw %s should carry the verbatim resource", items[0].Raw)
	}
	if len(rt.Calls()) != 2 {
		t.Fatalf("want 2 page requests, got %d", len(rt.Calls()))
	}
	first, second := rt.Calls()[0], rt.Calls()[1]
	if first.Method != http.MethodGet || !strings.Contains(first.URL, "/applications/com.example.app/subscriptions?") {
		t.Errorf("first request %s %s is not the subscriptions.list endpoint", first.Method, first.URL)
	}
	if strings.Contains(first.URL, "/edits/") {
		t.Errorf("url %q must not open an Edit", first.URL)
	}
	if !strings.Contains(second.URL, "pageToken=p2") {
		t.Errorf("second request %q must carry the nextPageToken", second.URL)
	}
}

// TestList_tokenLoopGuard asserts a server that repeats a nextPageToken forever
// surfaces an error instead of an infinite loop.
func TestList_tokenLoopGuard(t *testing.T) {
	rt := subsFake([]scripted{
		{200, `{"subscriptions":[{"productId":"a"}],"nextPageToken":"same"}`},
		{200, `{"subscriptions":[{"productId":"b"}],"nextPageToken":"same"}`},
		{200, `{"subscriptions":[{"productId":"c"}],"nextPageToken":"same"}`},
	})
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err == nil {
		t.Fatal("want token-loop error, got nil")
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Errorf("error %q should name the pagination loop", err)
	}
}

// TestList_emptyProductID_refuses asserts a malformed resource without a
// productId is rejected at the API boundary instead of becoming an
// unaddressable catalog entry.
func TestList_emptyProductID_refuses(t *testing.T) {
	rt := subsFake([]scripted{
		{200, `{"subscriptions":[{"listings":[{"languageCode":"en-US","title":"No ID"}]}]}`},
	})
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err == nil || !strings.Contains(err.Error(), "without a productId") {
		t.Fatalf("err = %v, want the missing-productId refusal", err)
	}
}

// TestCreate_postsResourceWithRegionsVersion asserts Create POSTs the file
// resource verbatim with productId and regionsVersion.version as query params.
func TestCreate_postsResourceWithRegionsVersion(t *testing.T) {
	rt := subsFake([]scripted{{200, `{"productId":"premium"}`}})
	body := json.RawMessage(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}]}`)
	raw, err := subscriptions.Create(context.Background(), client(rt), "com.example.app", "premium", "2022/02", body)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(rt.Calls()) != 1 {
		t.Fatalf("want 1 request, got %d", len(rt.Calls()))
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodPost || !strings.Contains(req.URL, "/applications/com.example.app/subscriptions?") {
		t.Errorf("request %s %s is not subscriptions.create", req.Method, req.URL)
	}
	if !strings.Contains(req.URL, "productId=premium") {
		t.Errorf("url %q must carry productId", req.URL)
	}
	if !strings.Contains(req.URL, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must carry regionsVersion.version", req.URL)
	}
	if string(req.Body) != string(body) {
		t.Errorf("body %q must be the resource verbatim", string(req.Body))
	}
	if !strings.Contains(string(raw), "premium") {
		t.Errorf("response %s should be returned verbatim", raw)
	}
}

// TestPatch_scopedUpdateMask asserts Patch PATCHes the addressed subscription
// with an updateMask restricted to the fields the walking skeleton manages, so
// unmanaged nesting (basePlans) can never be clobbered.
func TestPatch_scopedUpdateMask(t *testing.T) {
	rt := subsFake([]scripted{{200, `{"productId":"premium"}`}})
	body := json.RawMessage(`{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium+"}]}`)
	_, err := subscriptions.Patch(context.Background(), client(rt), "com.example.app", "premium", "2022/02", []string{"listings", "taxAndComplianceSettings"}, body)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodPatch || !strings.Contains(req.URL, "/applications/com.example.app/subscriptions/premium?") {
		t.Errorf("request %s %s is not subscriptions.patch", req.Method, req.URL)
	}
	if !strings.Contains(req.URL, "updateMask=listings%2CtaxAndComplianceSettings") {
		t.Errorf("url %q must scope the updateMask", req.URL)
	}
	if strings.Contains(req.URL, "basePlans") {
		t.Errorf("url %q must not include unmanaged fields in the mask", req.URL)
	}
	if !strings.Contains(req.URL, "regionsVersion.version=2022%2F02") {
		t.Errorf("url %q must carry regionsVersion.version", req.URL)
	}
}

// TestConvertRegionPrices_postsPrice asserts the helper POSTs the Money to the
// pricing:convertRegionPrices endpoint and returns the response verbatim.
func TestConvertRegionPrices_postsPrice(t *testing.T) {
	rt := subsFake([]scripted{{200, `{"regionVersion":{"version":"2022/02"},"convertedRegionPrices":{"FR":{"regionCode":"FR","price":{"currencyCode":"EUR","units":"4","nanos":490000000}}}}`}})
	raw, err := subscriptions.ConvertRegionPrices(context.Background(), client(rt), "com.example.app", subscriptions.Money{CurrencyCode: "USD", Units: "4", Nanos: 990000000})
	if err != nil {
		t.Fatalf("ConvertRegionPrices: %v", err)
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodPost || !strings.HasSuffix(strings.Split(req.URL, "?")[0], "/applications/com.example.app/pricing:convertRegionPrices") {
		t.Errorf("request %s %s is not monetization.convertRegionPrices", req.Method, req.URL)
	}
	if !strings.Contains(string(req.Body), `"units":"4"`) || !strings.Contains(string(req.Body), `"nanos":990000000`) {
		t.Errorf("body %q must carry the Money", string(req.Body))
	}
	if !strings.Contains(string(raw), "convertedRegionPrices") {
		t.Errorf("response %s should be returned verbatim", raw)
	}
}

// TestDelete_addressesProduct asserts Delete issues the DELETE and treats an
// empty 2xx as success.
func TestDelete_addressesProduct(t *testing.T) {
	rt := subsFake([]scripted{{204, ``}})
	if err := subscriptions.Delete(context.Background(), client(rt), "com.example.app", "legacy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	req := rt.Calls()[0]
	if req.Method != http.MethodDelete || !strings.HasSuffix(req.URL, "/applications/com.example.app/subscriptions/legacy") {
		t.Errorf("request %s %s is not subscriptions.delete", req.Method, req.URL)
	}
}

// TestErrorEnvelope_apiError asserts a non-2xx maps to *api.Error carrying the
// status and the operation, so commands can classify it.
func TestErrorEnvelope_apiError(t *testing.T) {
	rt := subsFake([]scripted{{403, `{"error":{"message":"The caller does not have permission"}}`}})
	_, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err %v is not *api.Error", err)
	}
	if apiErr.StatusCode != 403 || apiErr.Operation != "monetization.subscriptions.list" {
		t.Errorf("apiErr = {status:%d op:%s}, want 403 monetization.subscriptions.list", apiErr.StatusCode, apiErr.Operation)
	}
}
