package create_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
	createcmd "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/create"
)

func TestRun_happyPath_postsBuiltBodyWithScoreMinZero(t *testing.T) {
	var gotURL, gotBody string
	var stderr bytes.Buffer
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL = r.URL.String()
		var b bytes.Buffer
		if r.Body != nil {
			_, _ = b.ReadFrom(r.Body)
		}
		gotBody = b.String()
		return gamescmdtest.JSONResp(`{"id":"lb1","scoreOrder":"LARGER_IS_BETTER"}`), nil
	}))
	rc.Stderr = &stderr
	_, err := createcmd.Run(rc, createcmd.Input{
		ApplicationID: "77",
		Write: gamescmd.LeaderboardWrite{
			Name:        "High Scores",
			ScoreOrder:  "LARGER_IS_BETTER",
			ScoreMin:    0,
			ScoreMinSet: true,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gotURL, "/applications/77/leaderboards") {
		t.Errorf("url %q should address the application's leaderboards", gotURL)
	}
	// scoreMin 0 is a legitimate bound and must reach the wire (as a JSON string).
	if !strings.Contains(gotBody, `"scoreMin":"0"`) {
		t.Errorf("body %q should carry scoreMin:\"0\"", gotBody)
	}
	if !strings.Contains(gotBody, `"High Scores"`) {
		t.Errorf("body %q should carry the localized name", gotBody)
	}
	if !strings.Contains(stderr.String(), "✓") {
		t.Errorf("stderr = %q, want a ✓ confirmation line", stderr.String())
	}
}

func TestRun_dryRun_doesNotCall(t *testing.T) {
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		t.Fatalf("dry-run must not call the API: %s", r.URL)
		return nil, nil
	}))
	r, err := createcmd.Run(rc, createcmd.Input{ApplicationID: "77", Write: gamescmd.LeaderboardWrite{Name: "X"}, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"dryRun"`) {
		t.Errorf("dry-run json = %s, want a dryRun marker", out.String())
	}
}
