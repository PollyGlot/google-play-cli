package apply_test

import (
	"errors"
	"testing"

	applycmd "github.com/PollyGlot/google-play-cli/commands/iap/apply"
)

func TestRun_malformedLiveItemIn2xx_exit30(t *testing.T) {
	cases := map[string]iapRT{
		"one-time product field out of range": {
			v2Body: `{"oneTimeProducts":[{"productId":"coins100","x":1e400}]}`,
		},
		"purchase options not a list": {
			v2Body: `{"oneTimeProducts":[{"productId":"coins100","listings":[{"languageCode":"en-US","title":"Coins"}],"purchaseOptions":"x"}]}`,
		},
		"offer field out of range": {
			offersBody: `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","x":1e400}]}`,
		},
		"offer state not a string": {
			offersBody: `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","state":5,"regionalPricingAndAvailabilityConfigs":[{"regionCode":"US"}]}]}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt := &iapRT{v2Body: tc.v2Body, offersBody: tc.offersBody}
			_, err := applycmd.Run(newRC(t, rt), applycmd.Input{Package: "com.example.app", Dir: matchedCatalog(t), DryRun: true})
			if err == nil {
				t.Fatal("want error with exit 30, got nil")
			}
			var c interface{ ExitCode() int }
			if !errors.As(err, &c) || c.ExitCode() != 30 {
				t.Errorf("err = %v, want exit 30", err)
			}
		})
	}
}
