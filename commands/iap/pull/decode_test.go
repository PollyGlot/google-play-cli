package pull_test

import (
	"strings"
	"testing"

	pullcmd "github.com/PollyGlot/google-play-cli/commands/iap/pull"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRun_malformedLiveItemIn2xx_exit30(t *testing.T) {
	cases := map[string]struct {
		live iapLive
		op   string
	}{
		"one-time product": {
			live: iapLive{v2Body: `{"oneTimeProducts":[{"productId":"coins100","x":1e400}]}`},
			op:   "monetization.onetimeproducts.list",
		},
		"offer": {
			live: iapLive{
				v2Body:     `{"oneTimeProducts":[{"productId":"coins100","purchaseOptions":[{"purchaseOptionId":"buy"}]}]}`,
				offersBody: `{"oneTimeProductOffers":[{"productId":"coins100","purchaseOptionId":"buy","offerId":"promo","x":1e400}]}`,
			},
			op: "monetization.onetimeproducts.purchaseOptions.offers.list",
		},
		"legacy in-app product": {
			live: iapLive{legacyBody: `{"inappproduct":[{"sku":"old_gems","x":1e400}]}`},
			op:   "inappproducts.list",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := pullcmd.Run(newRC(t, testkit.NewFake(tc.live.serve)), pullcmd.Input{Package: "com.example.app", Dir: t.TempDir()})
			assertExit(t, err, 30)
			if !strings.HasPrefix(err.Error(), tc.op+" on com.example.app: ") {
				t.Errorf("err = %q, want it to name %s on the package", err, tc.op)
			}
		})
	}
}
