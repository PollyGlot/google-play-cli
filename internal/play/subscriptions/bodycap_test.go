package subscriptions_test

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
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// pricedRegions is roughly how many regions Play prices a subscription in:
// a catalog that sets a price everywhere carries one regional config each.
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
	return map[string]any{"currencyCode": currency, "units": "9", "nanos": 990000000}
}

// realisticSubscription is a subscription as a priced-everywhere catalog
// holds it: three auto-renewing base plans (weekly, monthly, yearly), each
// with a price in every region.
func realisticSubscription(i int) map[string]any {
	var plans []any
	for _, period := range []string{"P1W", "P1M", "P1Y"} {
		var regional []any
		for _, r := range regionCodes(pricedRegions) {
			regional = append(regional, map[string]any{"regionCode": r, "newSubscriberAvailability": true, "price": money("USD")})
		}
		plans = append(plans, map[string]any{
			"basePlanId": strings.ToLower("plan-" + period),
			"state":      "ACTIVE",
			"autoRenewingBasePlanType": map[string]any{
				"billingPeriodDuration": period, "gracePeriodDuration": "P7D", "accountHoldDuration": "P30D",
				"resubscribeState": "RESUBSCRIBE_STATE_ACTIVE", "prorationMode": "SUBSCRIPTION_PRORATION_MODE_CHARGE_ON_NEXT_BILLING_DATE", "legacyCompatible": true,
			},
			"regionalConfigs":    regional,
			"otherRegionsConfig": map[string]any{"usdPrice": money("USD"), "eurPrice": money("EUR"), "newSubscriberAvailability": true},
		})
	}
	return map[string]any{
		"packageName": "com.example.app",
		"productId":   fmt.Sprintf("premium_%03d", i),
		"listings": []any{
			map[string]any{"languageCode": "en-US", "title": "Premium", "benefits": []string{"No ads", "Offline mode"}, "description": "Every feature"},
			map[string]any{"languageCode": "fr-FR", "title": "Premium", "benefits": []string{"Sans publicité"}, "description": "Toutes les fonctions"},
		},
		"basePlans": plans,
	}
}

// realisticOffer is an intro offer priced everywhere: two phases with a
// regional discount each, plus its per-region availability.
func realisticOffer(i int) map[string]any {
	var phases []any
	for range 2 {
		var regional []any
		for _, r := range regionCodes(pricedRegions) {
			regional = append(regional, map[string]any{"regionCode": r, "relativeDiscount": 0.5})
		}
		phases = append(phases, map[string]any{"duration": "P1M", "recurrenceCount": 1, "regionalConfigs": regional, "otherRegionsConfig": map[string]any{"relativeDiscount": 0.5}})
	}
	var availability []any
	for _, r := range regionCodes(pricedRegions) {
		availability = append(availability, map[string]any{"regionCode": r, "newSubscriberAvailability": true})
	}
	return map[string]any{
		"packageName": "com.example.app", "productId": fmt.Sprintf("premium_%03d", i), "basePlanId": "plan-p1m", "offerId": "intro",
		"state": "ACTIVE", "phases": phases, "regionalConfigs": availability,
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

// pageSizeOf reads the pageSize the request asked for.
func pageSizeOf(t *testing.T, c testkit.Call) int {
	t.Helper()
	q, err := url.ParseQuery(c.Query)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(q.Get("pageSize"))
	if err != nil {
		t.Fatalf("request %s carries no numeric pageSize", c.URL)
	}
	return n
}

// fullPages answers each list request with as many realistic items as it
// asked for, twice: the page size gplay requests is what decides whether a
// full page of a priced-everywhere catalog fits the body cap (#790).
func fullPages(t *testing.T, field string, item func(int) map[string]any) *testkit.Fake {
	t.Helper()
	served := 0
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		n := pageSizeOf(t, c)
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

// TestList_fullRealisticPageFitsBodyCap asserts the page size List requests
// keeps a full page of priced-everywhere subscriptions under the 4 MiB body
// cap, so a large catalog lists instead of failing on its first page.
func TestList_fullRealisticPageFitsBodyCap(t *testing.T) {
	rt := fullPages(t, "subscriptions", realisticSubscription)
	items, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := 2 * pageSizeOf(t, rt.Calls()[0]); len(items) != want {
		t.Errorf("items = %d, want %d across both pages", len(items), want)
	}
}

// TestListAllOffers_fullRealisticPageFitsBodyCap is the same guarantee for
// the offers walk, which shares the page size.
func TestListAllOffers_fullRealisticPageFitsBodyCap(t *testing.T) {
	rt := fullPages(t, "subscriptionOffers", realisticOffer)
	if _, err := subscriptions.ListAllOffers(context.Background(), client(rt), "com.example.app"); err != nil {
		t.Fatalf("ListAllOffers: %v", err)
	}
}

// TestList_pageOverBodyCap_namesTheCap asserts a page past the body cap (100
// priced-everywhere subscriptions, the former page size) fails with an error
// naming the cap, never a parse of a truncated document.
func TestList_pageOverBodyCap_namesTheCap(t *testing.T) {
	items := make([]any, 100)
	for i := range items {
		items[i] = realisticSubscription(i)
	}
	body := playPage(t, "subscriptions", items, "")
	if len(body) <= api.MaxAPISuccessBodyRead {
		t.Fatalf("fixture is %d bytes, want past the %d-byte cap", len(body), api.MaxAPISuccessBodyRead)
	}
	rt := subsFake([]scripted{{200, body}})
	got, err := subscriptions.List(context.Background(), client(rt), "com.example.app")
	if err == nil {
		t.Fatalf("List returned %d items, want the body-cap error", len(got))
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Operation != "monetization.subscriptions.list" {
		t.Errorf("err = %#v, want an *api.Error on monetization.subscriptions.list", err)
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
