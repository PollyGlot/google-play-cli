package games_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/games"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const app = "123456789012"
	body := []byte(`{"achievementType":"STANDARD"}`)
	sends := []testkit.Send{
		{Name: "ListAchievements", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.ListAchievements(ctx, hc, app, 0, "")
			return []any{v, raw}, err
		}},
		{Name: "ListAchievements paged", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.ListAchievements(ctx, hc, app, 25, "tok/+=")
			return []any{v, raw}, err
		}},
		{Name: "GetAchievement", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.GetAchievement(ctx, hc, "ach 1")
			return []any{v, raw}, err
		}},
		{Name: "CreateAchievement", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.CreateAchievement(ctx, hc, app, body)
			return []any{v, raw}, err
		}},
		{Name: "UpdateAchievement", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.UpdateAchievement(ctx, hc, "ach1", body)
			return []any{v, raw}, err
		}},
		{Name: "DeleteAchievement", Fn: func(hc *http.Client) (any, error) { return nil, games.DeleteAchievement(ctx, hc, "ach1") }},
		{Name: "ListLeaderboards", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.ListLeaderboards(ctx, hc, app, 0, "")
			return []any{v, raw}, err
		}},
		{Name: "ListLeaderboards paged", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.ListLeaderboards(ctx, hc, app, 25, "tok/+=")
			return []any{v, raw}, err
		}},
		{Name: "GetLeaderboard", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.GetLeaderboard(ctx, hc, "lb1")
			return []any{v, raw}, err
		}},
		{Name: "CreateLeaderboard", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.CreateLeaderboard(ctx, hc, app, body)
			return []any{v, raw}, err
		}},
		{Name: "UpdateLeaderboard", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := games.UpdateLeaderboard(ctx, hc, "lb1", body)
			return []any{v, raw}, err
		}},
		{Name: "DeleteLeaderboard", Fn: func(hc *http.Client) (any, error) { return nil, games.DeleteLeaderboard(ctx, hc, "lb1") }},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
