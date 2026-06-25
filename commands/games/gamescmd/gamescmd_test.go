package gamescmd_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v does not carry an ExitCode", err)
	}
	return c.ExitCode()
}

func TestResolveApplicationID_emptyIsExit2(t *testing.T) {
	if _, err := gamescmd.ResolveApplicationID("  "); err == nil || exitCode(t, err) != 2 {
		t.Errorf("empty --application-id should be exit 2, got %v", err)
	}
	id, err := gamescmd.ResolveApplicationID(" 12345 ")
	if err != nil || id != "12345" {
		t.Errorf("ResolveApplicationID = %q, %v; want 12345", id, err)
	}
}

func TestRequireConfirm_missingIsExit3NamingFlag(t *testing.T) {
	err := gamescmd.RequireConfirm(false, "deleting is irreversible")
	if err == nil || exitCode(t, err) != 3 {
		t.Fatalf("missing --confirm should be exit 3, got %v", err)
	}
	var sfe interface{ Error() string }
	if !errors.As(err, &sfe) || !strings.Contains(err.Error(), "irreversible") {
		t.Errorf("error %v should carry the message", err)
	}
	if gamescmd.RequireConfirm(true, "x") != nil {
		t.Errorf("--confirm present should pass")
	}
}

func TestBuildAchievementBody_fromFlags_buildsLocalizedDraft(t *testing.T) {
	body, err := gamescmd.BuildAchievementBody(nil, gamescmd.AchievementWrite{
		Name:          "First Win",
		Description:   "Win a game",
		Type:          "STANDARD",
		InitialState:  "REVEALED",
		PointValue:    10,
		PointValueSet: true,
	}, true)
	if err != nil {
		t.Fatalf("BuildAchievementBody: %v", err)
	}
	var got games.AchievementConfiguration
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AchievementType != "STANDARD" || got.InitialState != "REVEALED" {
		t.Errorf("scalars = %+v", got)
	}
	if got.Draft == nil || got.Draft.PointValue != 10 {
		t.Fatalf("draft = %+v, want pointValue 10", got.Draft)
	}
	if got.Draft.Name.First() != "First Win" {
		t.Errorf("name = %q", got.Draft.Name.First())
	}
	if got.Draft.Description.First() != "Win a game" {
		t.Errorf("description = %q", got.Draft.Description.First())
	}
	// Default locale lands on en-US.
	if got.Draft.Name.Translations[0].Locale != "en-US" {
		t.Errorf("locale = %q, want en-US", got.Draft.Name.Translations[0].Locale)
	}
}

func TestBuildAchievementBody_fromJSONAndFlags_isExit2(t *testing.T) {
	_, err := gamescmd.BuildAchievementBody(nil, gamescmd.AchievementWrite{
		FromJSON: "-",
		Name:     "x",
	}, true)
	if err == nil || exitCode(t, err) != 2 {
		t.Errorf("combining --from-json with flags should be exit 2, got %v", err)
	}
}

func TestBuildAchievementBody_fromStdin_passthroughVerbatim(t *testing.T) {
	src := `{"achievementType":"INCREMENTAL","stepsToUnlock":5}`
	body, err := gamescmd.BuildAchievementBody(strings.NewReader(src), gamescmd.AchievementWrite{FromJSON: "-"}, true)
	if err != nil {
		t.Fatalf("BuildAchievementBody: %v", err)
	}
	if string(body) != src {
		t.Errorf("body = %s, want verbatim %s", body, src)
	}
}

func TestBuildAchievementBody_invalidJSON_isExit2(t *testing.T) {
	_, err := gamescmd.BuildAchievementBody(strings.NewReader("{not json"), gamescmd.AchievementWrite{FromJSON: "-"}, true)
	if err == nil || exitCode(t, err) != 2 {
		t.Errorf("invalid --from-json should be exit 2, got %v", err)
	}
}

func TestBuildAchievementBody_emptyRequireField_isExit2(t *testing.T) {
	_, err := gamescmd.BuildAchievementBody(nil, gamescmd.AchievementWrite{}, true)
	if err == nil || exitCode(t, err) != 2 {
		t.Errorf("empty body with requireField should be exit 2, got %v", err)
	}
}

func TestBuildLeaderboardBody_scoreMinZeroIsSent(t *testing.T) {
	body, err := gamescmd.BuildLeaderboardBody(nil, gamescmd.LeaderboardWrite{
		Name:        "High Scores",
		ScoreOrder:  "LARGER_IS_BETTER",
		ScoreMin:    0,
		ScoreMinSet: true,
		ScoreMax:    1000000,
		ScoreMaxSet: true,
	}, true)
	if err != nil {
		t.Fatalf("BuildLeaderboardBody: %v", err)
	}
	var got games.LeaderboardConfiguration
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ScoreOrder != "LARGER_IS_BETTER" {
		t.Errorf("scoreOrder = %q", got.ScoreOrder)
	}
	// An explicit 0 must be sent (it is a legitimate min), not dropped by omitempty.
	if got.ScoreMin != "0" {
		t.Errorf("scoreMin = %q, want \"0\"", got.ScoreMin)
	}
	if got.ScoreMax != "1000000" {
		t.Errorf("scoreMax = %q, want \"1000000\"", got.ScoreMax)
	}
	if got.Draft == nil || got.Draft.Name.First() != "High Scores" {
		t.Errorf("draft name = %+v", got.Draft)
	}
}

func TestColumns_resolveAndDefaults(t *testing.T) {
	if got := gamescmd.DefaultAchievementColumns(); got != "id,name,type,points,state" {
		t.Errorf("achievement default columns = %q", got)
	}
	if got := gamescmd.DefaultLeaderboardColumns(); got != "id,name,order,min,max" {
		t.Errorf("leaderboard default columns = %q", got)
	}
	if _, err := gamescmd.ResolveAchievementColumns("id,bogus"); err == nil || exitCode(t, err) != 2 {
		t.Errorf("unknown column should be exit 2, got %v", err)
	}
	cols, err := gamescmd.ResolveLeaderboardColumns("id,order")
	if err != nil || len(cols) != 2 {
		t.Errorf("ResolveLeaderboardColumns = %v, %v", cols, err)
	}
}

func TestBuildRows_readsDraftThenPublished(t *testing.T) {
	a := games.AchievementConfiguration{
		ID:              "a1",
		AchievementType: "STANDARD",
		Published:       &games.AchievementConfigurationDetail{PointValue: 5, Name: &games.LocalizedStringBundle{Translations: []games.LocalizedString{{Locale: "en-US", Value: "Pub"}}}},
	}
	row := gamescmd.BuildAchievementRow(a)
	if row.Name != "Pub" || row.Points != 5 {
		t.Errorf("row falls back to published: %+v", row)
	}
}
