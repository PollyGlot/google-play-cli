package iap_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/iap"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// pricedRegions is roughly how many regions Play prices a product in: a
// catalog that sets a price everywhere carries one regional config each.
const pricedRegions = 175

// regionCodes returns n distinct two-letter codes, the shape of a regionCode.
func regionCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		codes[i] = string(rune('A'+i/26)) + string(rune('A'+i%26))
	}
	return codes
}

func money(currency string) map[string]any {
	return map[string]any{"currencyCode": currency, "units": "4", "nanos": 990000000}
}

// realisticProduct is a one-time product as a priced-everywhere catalog
// holds it: a buy and a rent purchase option, each with a price in every
// region.
func realisticProduct(i int) map[string]any {
	var options []any
	for _, id := range []string{"buy", "rent"} {
		var regional []any
		for _, r := range regionCodes(pricedRegions) {
			regional = append(regional, map[string]any{"regionCode": r, "price": money("USD"), "availability": "AVAILABLE"})
		}
		option := map[string]any{
			"purchaseOptionId": id, "state": "ACTIVE",
			"regionalPricingAndAvailabilityConfigs": regional,
			"newRegionsConfig":                      map[string]any{"usdPrice": money("USD"), "eurPrice": money("EUR"), "availability": "AVAILABLE"},
		}
		if id == "buy" {
			option["buyOption"] = map[string]any{"legacyCompatible": true, "multiQuantityEnabled": false}
		} else {
			option["rentOption"] = map[string]any{"rentalPeriod": "P30D", "expirationPeriod": "P2D"}
		}
		options = append(options, option)
	}
	return map[string]any{
		"packageName": "com.example.app",
		"productId":   fmt.Sprintf("coins_%03d", i),
		"listings": []any{
			map[string]any{"languageCode": "en-US", "title": "Coins", "description": "A bag of coins"},
			map[string]any{"languageCode": "fr-FR", "title": "Pièces", "description": "Un sac de pièces"},
		},
		"purchaseOptions": options,
	}
}

// realisticOffer is a discounted offer priced everywhere.
func realisticOffer(i int) map[string]any {
	var regional []any
	for _, r := range regionCodes(pricedRegions) {
		regional = append(regional, map[string]any{"regionCode": r, "relativeDiscount": 0.25, "availability": "AVAILABLE"})
	}
	return map[string]any{
		"packageName": "com.example.app", "productId": fmt.Sprintf("coins_%03d", i), "purchaseOptionId": "buy", "offerId": "sale",
		"state": "ACTIVE", "discountedOffer": map[string]any{"startTime": "2026-11-01T00:00:00Z", "endTime": "2026-12-01T00:00:00Z"},
		"regionalPricingAndAvailabilityConfigs": regional,
	}
}

// playPage renders a list page as Play sends it: pretty-printed, Play's
// default (Discovery: prettyPrint defaults to true, and gplay does not turn
// it off), which roughly doubles the bytes of the compact form.
func playPage(t *testing.T, field string, items []any, next string) string {
	t.Helper()
	page := map[string]any{field: items}
	if next != "" {
		page["nextPageToken"] = next
	}
	b, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fullPages answers each list request with as many realistic items as it
// asked for, twice: the page size gplay requests is what decides whether a
// full page of a priced-everywhere catalog fits the body cap (#790).
func fullPages(t *testing.T, field string, item func(int) map[string]any) *testkit.Fake {
	t.Helper()
	served := 0
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		q, err := url.ParseQuery(c.Query)
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(q.Get("pageSize"))
		if err != nil {
			t.Fatalf("request %s carries no numeric pageSize", c.URL)
		}
		items := make([]any, n)
		for i := range items {
			items[i] = item(served + i)
		}
		served += n
		next := ""
		if served == n {
			next = "p2"
		}
		body := playPage(t, field, items, next)
		if len(body) > api.MaxAPISuccessBodyRead {
			t.Errorf("a full page of %d realistic items is %d bytes, past the %d-byte body cap", n, len(body), api.MaxAPISuccessBodyRead)
		}
		return 200, body, true
	})
}

// TestListOneTimeProducts_fullRealisticPageFitsBodyCap asserts the page size
// the v2 list requests keeps a full page of priced-everywhere products under
// the 4 MiB body cap.
func TestListOneTimeProducts_fullRealisticPageFitsBodyCap(t *testing.T) {
	rt := fullPages(t, "oneTimeProducts", realisticProduct)
	if _, err := iap.ListOneTimeProducts(context.Background(), client(rt), "com.example.app"); err != nil {
		t.Fatalf("ListOneTimeProducts: %v", err)
	}
	if len(rt.Calls()) != 2 {
		t.Errorf("calls = %d, want both pages", len(rt.Calls()))
	}
}

// TestListAllOffers_fullRealisticPageFitsBodyCap is the same guarantee for
// the offers walk, which shares the page size.
func TestListAllOffers_fullRealisticPageFitsBodyCap(t *testing.T) {
	rt := fullPages(t, "oneTimeProductOffers", realisticOffer)
	if _, err := iap.ListAllOffers(context.Background(), client(rt), "com.example.app"); err != nil {
		t.Fatalf("ListAllOffers: %v", err)
	}
}

// TestListOneTimeProducts_pageOverBodyCap_namesTheCap asserts a page past the
// body cap (100 priced-everywhere products, the former page size) fails with
// an error naming the cap, never a parse of a truncated document.
func TestListOneTimeProducts_pageOverBodyCap_namesTheCap(t *testing.T) {
	items := make([]any, 100)
	for i := range items {
		items[i] = realisticProduct(i)
	}
	body := playPage(t, "oneTimeProducts", items, "")
	if len(body) <= api.MaxAPISuccessBodyRead {
		t.Fatalf("fixture is %d bytes, want past the %d-byte cap", len(body), api.MaxAPISuccessBodyRead)
	}
	rt := iapFake([]scripted{{200, body}})
	got, err := iap.ListOneTimeProducts(context.Background(), client(rt), "com.example.app")
	if err == nil {
		t.Fatalf("ListOneTimeProducts returned %d items, want the body-cap error", len(got))
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Operation != "monetization.onetimeproducts.list" {
		t.Errorf("err = %#v, want an *api.Error on monetization.onetimeproducts.list", err)
	}
	for _, want := range []string{"4 MiB", "refusing to truncate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the cap (%q)", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "decode") || strings.Contains(err.Error(), "unexpected end") {
		t.Errorf("error %q reads as a truncated parse", err.Error())
	}
}
