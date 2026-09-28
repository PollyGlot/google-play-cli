// Migration proof for #518: subscriptions (and its offers half) now take their
// verbs and URLs from internal/apiregistry instead of local literals. The
// pre-existing tests are untouched and still assert the behaviour; what is
// added here is the part they only checked by substring, the ABSOLUTE URL
// (host included) and the verb of every call, custom `:activate` /
// `:deactivate` / `:migratePrices` verbs included.
package subscriptions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

// pinned answers {} to every call and records what was sent.
func pinned() *testkit.Fake { return testkit.NewFake(testkit.Any(http.StatusOK, `{}`)) }

// first is the first call f recorded, or the zero Call when none was sent.
func first(f *testkit.Fake) testkit.Call {
	if calls := f.Calls(); len(calls) > 0 {
		return calls[0]
	}
	return testkit.Call{}
}

const base = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app"

func TestResolvedURLsUnchanged(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(*http.Client) error
		verb string
		want string
	}{
		{
			name: "subscriptions.list",
			call: func(hc *http.Client) error { _, err := subscriptions.List(ctx, hc, "com.example.app"); return err },
			verb: http.MethodGet,
			want: base + "/subscriptions?pageSize=100",
		},
		{
			name: "subscriptions.create",
			call: func(hc *http.Client) error {
				_, err := subscriptions.Create(ctx, hc, "com.example.app", "premium", "2022/02", json.RawMessage(`{}`))
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions?productId=premium&regionsVersion.version=2022%2F02",
		},
		{
			name: "subscriptions.patch",
			call: func(hc *http.Client) error {
				_, err := subscriptions.Patch(ctx, hc, "com.example.app", "premium", "2022/02", []string{"listings"}, json.RawMessage(`{}`))
				return err
			},
			verb: http.MethodPatch,
			want: base + "/subscriptions/premium?regionsVersion.version=2022%2F02&updateMask=listings",
		},
		{
			name: "subscriptions.delete",
			call: func(hc *http.Client) error { return subscriptions.Delete(ctx, hc, "com.example.app", "premium") },
			verb: http.MethodDelete,
			want: base + "/subscriptions/premium",
		},
		{
			name: "convertRegionPrices",
			call: func(hc *http.Client) error {
				_, err := subscriptions.ConvertRegionPrices(ctx, hc, "com.example.app", subscriptions.Money{CurrencyCode: "EUR", Units: "5"})
				return err
			},
			verb: http.MethodPost,
			want: base + "/pricing:convertRegionPrices",
		},
		{
			name: "basePlans.offers.list (wildcard walk)",
			call: func(hc *http.Client) error {
				_, err := subscriptions.ListAllOffers(ctx, hc, "com.example.app")
				return err
			},
			verb: http.MethodGet,
			want: base + "/subscriptions/-/basePlans/-/offers?pageSize=100",
		},
		{
			name: "basePlans.offers.create",
			call: func(hc *http.Client) error {
				_, err := subscriptions.CreateOffer(ctx, hc, "com.example.app", "premium", "monthly", "intro", "2022/02", json.RawMessage(`{}`))
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly/offers?offerId=intro&regionsVersion.version=2022%2F02",
		},
		{
			name: "basePlans.offers.patch",
			call: func(hc *http.Client) error {
				_, err := subscriptions.PatchOffer(ctx, hc, "com.example.app", "premium", "monthly", "intro", "2022/02", []string{"phases"}, json.RawMessage(`{}`))
				return err
			},
			verb: http.MethodPatch,
			want: base + "/subscriptions/premium/basePlans/monthly/offers/intro?regionsVersion.version=2022%2F02&updateMask=phases",
		},
		{
			name: "basePlans.offers.delete",
			call: func(hc *http.Client) error {
				return subscriptions.DeleteOffer(ctx, hc, "com.example.app", "premium", "monthly", "intro")
			},
			verb: http.MethodDelete,
			want: base + "/subscriptions/premium/basePlans/monthly/offers/intro",
		},
		{
			name: "basePlans.activate",
			call: func(hc *http.Client) error {
				_, err := subscriptions.SetBasePlanState(ctx, hc, "com.example.app", "premium", "monthly", true)
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly:activate",
		},
		{
			name: "basePlans.deactivate",
			call: func(hc *http.Client) error {
				_, err := subscriptions.SetBasePlanState(ctx, hc, "com.example.app", "premium", "monthly", false)
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly:deactivate",
		},
		{
			name: "basePlans.offers.activate",
			call: func(hc *http.Client) error {
				_, err := subscriptions.SetOfferState(ctx, hc, "com.example.app", "premium", "monthly", "intro", true)
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly/offers/intro:activate",
		},
		{
			name: "basePlans.offers.deactivate",
			call: func(hc *http.Client) error {
				_, err := subscriptions.SetOfferState(ctx, hc, "com.example.app", "premium", "monthly", "intro", false)
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly/offers/intro:deactivate",
		},
		{
			name: "basePlans.migratePrices",
			call: func(hc *http.Client) error {
				_, err := subscriptions.MigrateBasePlanPrices(ctx, hc, "com.example.app", "premium", "monthly", subscriptions.MigrateBasePlanPricesRequest{})
				return err
			},
			verb: http.MethodPost,
			want: base + "/subscriptions/premium/basePlans/monthly:migratePrices",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := pinned()
			if err := tc.call(&http.Client{Transport: rt}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if first(rt).URL != tc.want {
				t.Errorf("URL = %q, want %q", first(rt).URL, tc.want)
			}
			if first(rt).Method != tc.verb {
				t.Errorf("verb = %q, want %q", first(rt).Method, tc.verb)
			}
		})
	}
}

// TestEmptyBasePlanFailsBeforeTheWire asserts a missing path parameter is
// refused locally, as an *api.Error, rather than sent as a truncated URL that
// would 404 far from its cause.
func TestEmptyBasePlanFailsBeforeTheWire(t *testing.T) {
	rt := pinned()
	_, err := subscriptions.SetBasePlanState(context.Background(), &http.Client{Transport: rt}, "com.example.app", "premium", "", true)
	if err == nil {
		t.Fatal("SetBasePlanState with an empty base plan succeeded, want an error")
	}
	if first(rt).URL != "" {
		t.Errorf("a request was sent (%q); the missing parameter must be caught before the wire", first(rt).URL)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if !strings.Contains(apiErr.Message, `"basePlanId"`) {
		t.Errorf("message = %q, want it to name the basePlanId parameter", apiErr.Message)
	}
}
