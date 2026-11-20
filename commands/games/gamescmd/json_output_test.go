package gamescmd_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

const achievementBody = `{"achievementType":"STANDARD","initialState":"REVEALED","draft":{"name":{"translations":[{"locale":"en-US","value":"First <Boss> & Beyond"}]},"pointValue":10}}`

func TestRenderJSON_achievementWriteDryRun_golden(t *testing.T) {
	p := gamescmd.AchievementWritePayload{Verb: "create achievement", Target: "application 123456789012", DryRun: true, Body: json.RawMessage(achievementBody)}
	outputtest.GoldenJSON(t, "achievement_write_dry_run.json.golden", p)
}

func TestRenderJSON_leaderboardWriteDryRun_golden(t *testing.T) {
	body := `{"scoreOrder":"LARGER_IS_BETTER","scoreMin":"0","scoreMax":"100000"}`
	p := gamescmd.LeaderboardWritePayload{Verb: "set leaderboard", Target: "leaderboard CgkI-example", DryRun: true, Body: json.RawMessage(body)}
	outputtest.GoldenJSON(t, "leaderboard_write_dry_run.json.golden", p)
}

func TestRenderJSON_flagBuiltBodyDryRun_golden(t *testing.T) {
	body, err := gamescmd.BuildLeaderboardBody(nil, gamescmd.LeaderboardWrite{Name: "Boss <Rush> & Co"}, true)
	if err != nil {
		t.Fatalf("BuildLeaderboardBody: %v", err)
	}
	p := gamescmd.LeaderboardWritePayload{Verb: "create leaderboard", Target: "application 123456789012", DryRun: true, Body: body}
	outputtest.GoldenJSON(t, "leaderboard_flags_dry_run.json.golden", p)
}

func TestRenderJSON_deleteDryRun_golden(t *testing.T) {
	p := gamescmd.DeletePayload{Kind: "achievement", ID: "CgkI-example-ach", DryRun: true}
	outputtest.GoldenJSON(t, "delete_dry_run.json.golden", p)
}

func TestRenderJSON_deleted_golden(t *testing.T) {
	p := gamescmd.DeletePayload{Kind: "leaderboard", ID: "CgkI-example-lb"}
	outputtest.GoldenJSON(t, "deleted.json.golden", p)
}
