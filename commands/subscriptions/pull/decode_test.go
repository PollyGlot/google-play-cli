package pull_test

import (
	"net/http"
	"strings"
	"testing"

	pullcmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/pull"
)

func TestRun_malformedLiveItemIn2xx_exit30(t *testing.T) {
	cases := map[string]struct {
		rt *subsRT
		op string
	}{
		"subscription": {
			rt: &subsRT{status: http.StatusOK, body: `{"subscriptions":[{"productId":"premium","x":1e400}]}`},
			op: "monetization.subscriptions.list",
		},
		"offer": {
			rt: &subsRT{offersBody: `{"subscriptionOffers":[{"productId":"premium","basePlanId":"monthly","offerId":"intro","x":1e400}]}`},
			op: "monetization.subscriptions.basePlans.offers.list",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := pullcmd.Run(newRC(t, tc.rt), pullcmd.Input{Package: "com.example.app", Dir: t.TempDir()})
			assertExit(t, err, 30)
			if !strings.HasPrefix(err.Error(), tc.op+" on com.example.app: ") {
				t.Errorf("err = %q, want it to name %s on the package", err, tc.op)
			}
		})
	}
}
