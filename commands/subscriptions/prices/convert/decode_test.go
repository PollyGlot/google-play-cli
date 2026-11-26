package convert_test

import (
	"net/http"
	"testing"

	convertcmd "github.com/PollyGlot/google-play-cli/commands/subscriptions/prices/convert"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRun_undecodable2xxBody_exit30(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"convertedRegionPrices":"x"}`))
	rc := newRC(t, fake)
	_, err := convertcmd.Run(rc, convertcmd.Input{Package: "com.example.app", Price: "4.99", Currency: "USD"})
	assertExit(t, err, 30)
}
