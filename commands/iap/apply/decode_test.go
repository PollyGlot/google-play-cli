package apply_test

import (
	"errors"
	"strings"
	"testing"

	applycmd "github.com/PollyGlot/google-play-cli/commands/iap/apply"
)

func TestRun_malformedLiveItemIn2xx_exit30(t *testing.T) {
	const (
		opList       = "monetization.onetimeproducts.list"
		opOffersList = "monetization.onetimeproducts.purchaseOptions.offers.list"
		opLegacyList = "inappproducts.list"
	)
	cases := map[string]struct{ v2Body, offersBody, legacyBody, op string }{
		"one-time product field out of range": {
			v2Body: `{"oneTimeProducts":[{"productId":"coins100","x":1e400}]}`,
			op:     opList,
		},
		"purchase options not a list": {
			v2Body: `{"oneTimeProducts":[{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":"x"}]}`,
			op:     opList,
		},
		"offer field out of range": {
			offersBody: `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","x":1e400}]}`,
			op:         opOffersList,
		},
		"offer state not a string": {
			offersBody: `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":5,"regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}`,
			op:         opOffersList,
		},
		"legacy in-app product field out of range": {
			legacyBody: `{"inappproduct":[{"sku":"old_gems","purchaseType":"managedUser","status":"active","x":1e400}]}`,
			op:         opLegacyList,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt := &iapRT{v2Body: tc.v2Body, offersBody: tc.offersBody, legacyBody: tc.legacyBody}
			_, err := applycmd.Run(newRC(t, rt), applycmd.Input{Package: "com.example.app", Dir: matchedCatalog(t), DryRun: true})
			if err == nil {
				t.Fatal("want error with exit 30, got nil")
			}
			var c interface{ ExitCode() int }
			if !errors.As(err, &c) || c.ExitCode() != 30 {
				t.Errorf("err = %v, want exit 30", err)
			}
			if !strings.HasPrefix(err.Error(), tc.op+" on com.example.app: ") {
				t.Errorf("err = %q, want it to name %s on the package", err, tc.op)
			}
		})
	}
}
