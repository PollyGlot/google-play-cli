package update_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
	updatecmd "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/update"
)

func TestRun_happyPath_putsToResource(t *testing.T) {
	var gotURL, gotMethod string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL, gotMethod = r.URL.String(), r.Method
		return gamescmdtest.JSONResp(`{"id":"l9"}`), nil
	}))
	_, err := updatecmd.Run(rc, updatecmd.Input{ID: "l9", Write: gamescmd.LeaderboardWrite{ScoreOrder: "SMALLER_IS_BETTER"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/leaderboards/l9") {
		t.Errorf("url %q should address leaderboard l9", gotURL)
	}
}
