package apply_test

import (
	"testing"

	applycmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/apply"
)

func TestRun_malformedLiveItemIn2xx_exit30(t *testing.T) {
	cases := map[string]struct{ live, offers string }{
		"subscription field out of range": {
			live: `{"subscriptions":[{"productId":"premium","x":1e400}]}`,
		},
		"declared subscription with base plans not a list": {
			live: `{"subscriptions":[{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":"x"}]}`,
		},
		"declared subscription with a base plan without an id": {
			live: `{"subscriptions":[{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"state":"ACTIVE"}]}]}`,
		},
		"undeclared subscription with base plans not a list": {
			live: `{"subscriptions":[{"productId":"premium","listings":[{"languageCode":"en-US","title":"Premium"}],"basePlans":[{"basePlanId":"monthly"}]},{"productId":"gone","listings":[{"languageCode":"en-US","title":"Gone"}]},{"productId":"extra","basePlans":"x"}]}`,
		},
		"offer state not a string": {
			offers: `{"subscriptionOffers":[{"productId":"premium","basePlanId":"monthly","offerId":"intro","state":5}]}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rc, _ := newRC(t, &subsRT{liveBody: tc.live, offersBody: tc.offers})
			_, err := applycmd.Run(rc, applycmd.Input{Package: "com.example.app", Dir: matchedCatalog(t), DryRun: true})
			assertExit(t, err, 30)
		})
	}
}
