// Migration proof for #519: games now takes verb and URL from
// internal/apiregistry. The pre-existing games_test.go asserts the path
// fragments and the paging params; what it never checked is the HOST, which
// matters more here than anywhere else because gamesConfiguration is a
// separate Google service (gamesconfiguration.googleapis.com, not
// androidpublisher). This file pins the absolute URL and verb of all ten
// methods, so a resolver or snapshot change cannot quietly move the calls to
// another host.
package games_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

const gamesRoot = "https://gamesconfiguration.googleapis.com/games/v1configuration"

// TestAbsoluteURLsUnchanged drives every exported call against a recorder and
// compares the whole URL, host included, with the pre-migration shape.
func TestAbsoluteURLsUnchanged(t *testing.T) {
	cases := []struct {
		name     string
		call     func(*http.Client) error
		wantURL  string
		wantVerb string
	}{
		{
			name: "achievements list",
			call: func(hc *http.Client) error {
				_, _, err := games.ListAchievements(context.Background(), hc, "12345", 0, "")
				return err
			},
			wantURL:  gamesRoot + "/applications/12345/achievements",
			wantVerb: http.MethodGet,
		},
		{
			name: "achievements get",
			call: func(hc *http.Client) error {
				_, _, err := games.GetAchievement(context.Background(), hc, "ach-1")
				return err
			},
			wantURL:  gamesRoot + "/achievements/ach-1",
			wantVerb: http.MethodGet,
		},
		{
			name: "achievements insert",
			call: func(hc *http.Client) error {
				_, _, err := games.CreateAchievement(context.Background(), hc, "12345", []byte(`{}`))
				return err
			},
			wantURL:  gamesRoot + "/applications/12345/achievements",
			wantVerb: http.MethodPost,
		},
		{
			name: "achievements update",
			call: func(hc *http.Client) error {
				_, _, err := games.UpdateAchievement(context.Background(), hc, "ach-1", []byte(`{}`))
				return err
			},
			wantURL:  gamesRoot + "/achievements/ach-1",
			wantVerb: http.MethodPut,
		},
		{
			name:     "achievements delete",
			call:     func(hc *http.Client) error { return games.DeleteAchievement(context.Background(), hc, "ach-1") },
			wantURL:  gamesRoot + "/achievements/ach-1",
			wantVerb: http.MethodDelete,
		},
		{
			name: "leaderboards list",
			call: func(hc *http.Client) error {
				_, _, err := games.ListLeaderboards(context.Background(), hc, "12345", 0, "")
				return err
			},
			wantURL:  gamesRoot + "/applications/12345/leaderboards",
			wantVerb: http.MethodGet,
		},
		{
			name: "leaderboards get",
			call: func(hc *http.Client) error {
				_, _, err := games.GetLeaderboard(context.Background(), hc, "lb-1")
				return err
			},
			wantURL:  gamesRoot + "/leaderboards/lb-1",
			wantVerb: http.MethodGet,
		},
		{
			name: "leaderboards insert",
			call: func(hc *http.Client) error {
				_, _, err := games.CreateLeaderboard(context.Background(), hc, "12345", []byte(`{}`))
				return err
			},
			wantURL:  gamesRoot + "/applications/12345/leaderboards",
			wantVerb: http.MethodPost,
		},
		{
			name: "leaderboards update",
			call: func(hc *http.Client) error {
				_, _, err := games.UpdateLeaderboard(context.Background(), hc, "lb-1", []byte(`{}`))
				return err
			},
			wantURL:  gamesRoot + "/leaderboards/lb-1",
			wantVerb: http.MethodPut,
		},
		{
			name:     "leaderboards delete",
			call:     func(hc *http.Client) error { return games.DeleteLeaderboard(context.Background(), hc, "lb-1") },
			wantURL:  gamesRoot + "/leaderboards/lb-1",
			wantVerb: http.MethodDelete,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotURL, gotVerb, body string
			hc := recorder(jsonResp(200, `{}`), &gotURL, &gotVerb, &body)
			if err := tc.call(hc); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Errorf("URL = %q, want %q", gotURL, tc.wantURL)
			}
			if gotVerb != tc.wantVerb {
				t.Errorf("verb = %q, want %q", gotVerb, tc.wantVerb)
			}
		})
	}
}
