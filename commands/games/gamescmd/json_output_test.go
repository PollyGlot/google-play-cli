package gamescmd_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// achievementBody is a --from-json request body, kept byte-for-byte by the
// reader: its & and <> must reach the preview unescaped.
const achievementBody = `{"achievementType":"STANDARD","initialState":"REVEALED","draft":{"name":{"translations":[{"locale":"en-US","value":"First <Boss> & Beyond"}]},"pointValue":10}}`

// TestRenderJSON_achievementWriteDryRun_golden freezes the --dry-run preview
// of a create, whose body is the exact request that would be sent. The live
// path passes the API body through (ADR-0003) and has no golden.
func TestRenderJSON_achievementWriteDryRun_golden(t *testing.T) {
	p := gamescmd.AchievementWritePayload{Verb: "create achievement", Target: "application 123456789012", DryRun: true, Body: json.RawMessage(achievementBody)}
	outputtest.GoldenJSON(t, "achievement_write_dry_run.json.golden", p)
}

// TestRenderJSON_leaderboardWriteDryRun_golden freezes the same preview for
// the leaderboard payload, a separate Renderable over the shared encoder.
func TestRenderJSON_leaderboardWriteDryRun_golden(t *testing.T) {
	body := `{"scoreOrder":"LARGER_IS_BETTER","scoreMin":"0","scoreMax":"100000"}`
	p := gamescmd.LeaderboardWritePayload{Verb: "update leaderboard", Target: "leaderboard CgkI-example", DryRun: true, Body: json.RawMessage(body)}
	outputtest.GoldenJSON(t, "leaderboard_write_dry_run.json.golden", p)
}

// TestRenderJSON_deleteDryRun_golden freezes the delete preview: deleted is
// omitted and requires names the --confirm gate.
func TestRenderJSON_deleteDryRun_golden(t *testing.T) {
	p := gamescmd.DeletePayload{Kind: "achievement", ID: "CgkI-example-ach", DryRun: true}
	outputtest.GoldenJSON(t, "delete_dry_run.json.golden", p)
}

// TestRenderJSON_deleted_golden freezes the synthesized delete result: the API
// returns no body, so this shape is gplay's own.
func TestRenderJSON_deleted_golden(t *testing.T) {
	p := gamescmd.DeletePayload{Kind: "leaderboard", ID: "CgkI-example-lb"}
	outputtest.GoldenJSON(t, "deleted.json.golden", p)
}
