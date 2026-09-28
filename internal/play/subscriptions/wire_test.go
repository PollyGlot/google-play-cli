package subscriptions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	body := json.RawMessage(`{"productId":"gold","listings":[{"languageCode":"en-US","title":"Gold"}]}`)
	sends := []testkit.Send{
		{Name: "Create", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.Create(ctx, hc, pkg, "gold", "2026/09", body)
			return v, err
		}},
		{Name: "Patch", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.Patch(ctx, hc, pkg, "gold", "2026/09", []string{"listings", "basePlans"}, body)
			return v, err
		}},
		{Name: "Delete", Fn: func(hc *http.Client) (any, error) { return nil, subscriptions.Delete(ctx, hc, pkg, "gold") }},
		{Name: "ConvertRegionPrices", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.ConvertRegionPrices(ctx, hc, pkg, subscriptions.Money{CurrencyCode: "EUR", Units: "4", Nanos: 990000000})
			return v, err
		}},
		{Name: "CreateOffer", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.CreateOffer(ctx, hc, pkg, "gold", "monthly", "intro", "2026/09", body)
			return v, err
		}},
		{Name: "PatchOffer", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.PatchOffer(ctx, hc, pkg, "gold", "monthly", "intro", "2026/09", []string{"phases"}, body)
			return v, err
		}},
		{Name: "DeleteOffer", Fn: func(hc *http.Client) (any, error) {
			return nil, subscriptions.DeleteOffer(ctx, hc, pkg, "gold", "monthly", "intro")
		}},
		{Name: "SetBasePlanState activate", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.SetBasePlanState(ctx, hc, pkg, "gold", "monthly", true)
			return v, err
		}},
		{Name: "SetBasePlanState deactivate", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.SetBasePlanState(ctx, hc, pkg, "gold", "monthly", false)
			return v, err
		}},
		{Name: "DeleteBasePlan", Fn: func(hc *http.Client) (any, error) {
			return nil, subscriptions.DeleteBasePlan(ctx, hc, pkg, "gold", "monthly")
		}},
		{Name: "SetOfferState activate", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.SetOfferState(ctx, hc, pkg, "gold", "monthly", "intro", true)
			return v, err
		}},
		{Name: "SetOfferState deactivate", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.SetOfferState(ctx, hc, pkg, "gold", "monthly", "intro", false)
			return v, err
		}},
		{Name: "MigrateBasePlanPrices", Fn: func(hc *http.Client) (any, error) {
			v, err := subscriptions.MigrateBasePlanPrices(ctx, hc, pkg, "gold", "monthly", subscriptions.MigrateBasePlanPricesRequest{
				PackageName: pkg, ProductID: "gold", BasePlanID: "monthly",
				RegionalPriceMigrations: []subscriptions.RegionalPriceMigration{{RegionCode: "FR", OldestAllowedPriceVersionTime: "2026-01-01T00:00:00Z"}},
				RegionsVersion:          subscriptions.RegionsVersion{Version: "2026/09"},
			})
			return v, err
		}},
		{Name: "List", Fn: func(hc *http.Client) (any, error) { v, err := subscriptions.List(ctx, hc, pkg); return v, err }},
		{Name: "ListAllOffers", Fn: func(hc *http.Client) (any, error) { v, err := subscriptions.ListAllOffers(ctx, hc, pkg); return v, err }},
	}
	var b strings.Builder
	b.WriteString(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))

	// The two listings follow nextPageToken to the end and refuse a token the
	// server already sent; each run needs its own page sequence.
	for _, l := range []struct {
		name, key, item string
		fn              func(*http.Client) (any, error)
	}{
		{"List", "subscriptions", `{"productId":"gold"}`, sends[len(sends)-2].Fn},
		{"ListAllOffers", "subscriptionOffers", `{"productId":"gold","basePlanId":"monthly","offerId":"intro"}`, sends[len(sends)-1].Fn},
	} {
		page := func(next string) string {
			return `{"` + l.key + `":[` + l.item + `],"nextPageToken":"` + next + `"}`
		}
		b.WriteString(testkit.Exchanges([]testkit.Send{{Name: l.name, Fn: l.fn}},
			testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(page("p/2+"), page(""))}}))
		b.WriteString(testkit.Exchanges([]testkit.Send{{Name: l.name, Fn: l.fn}},
			testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(page("again"))}}))
		b.WriteString(testkit.Exchanges([]testkit.Send{{Name: l.name, Fn: l.fn}},
			testkit.Answer{Name: "anonymous item", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{"`+l.key+`":[{}]}`)}}))
	}
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
