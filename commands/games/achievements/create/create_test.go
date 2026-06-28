package create_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	createcmd "github.com/PollyGlot/google-play-cli/commands/games/achievements/create"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
)

func TestRun_dryRun_previewsBodyAndDoesNotCall(t *testing.T) {
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		t.Fatalf("dry-run must not call the API: %s", r.URL)
		return nil, nil
	}))
	r, err := createcmd.Run(rc, createcmd.Input{
		ApplicationID: "999",
		Write:         gamescmd.AchievementWrite{Name: "Win", Type: "STANDARD"},
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	// The dry-run envelope embeds the exact body that would be sent.
	var got struct {
		DryRun bool            `json:"dryRun"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal dry-run view: %v (%s)", err, out.String())
	}
	if !got.DryRun || !strings.Contains(string(got.Body), `"Win"`) {
		t.Errorf("dry-run view = %s, want the request body with the name", out.String())
	}
}

func TestRun_happyPath_postsAndConfirms(t *testing.T) {
	var gotURL, gotBody string
	var stderr bytes.Buffer
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL = r.URL.String()
		b, _ := readAll(r)
		gotBody = b
		return gamescmdtest.JSONResp(`{"id":"new1","achievementType":"STANDARD"}`), nil
	}))
	rc.Stderr = &stderr
	r, err := createcmd.Run(rc, createcmd.Input{
		ApplicationID: "999",
		Write:         gamescmd.AchievementWrite{Name: "Win", Type: "STANDARD", PointValue: 10, PointValueSet: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gotURL, "/applications/999/achievements") {
		t.Errorf("url %q should address the application's achievements", gotURL)
	}
	if !strings.Contains(gotBody, `"pointValue":10`) || !strings.Contains(gotBody, `"Win"`) {
		t.Errorf("body %q should carry the built draft", gotBody)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"new1"`) {
		t.Errorf("json = %s, want the created resource verbatim", out.String())
	}
	if !strings.Contains(stderr.String(), "✓") {
		t.Errorf("stderr = %q, want a ✓ confirmation line", stderr.String())
	}
}

// TestRun_explicitZeroFlags_reachTheBody is the command-level regression guard
// for the *int fix: an explicit --point-value 0 / --steps-to-unlock 0 (the *Set
// bits flipped, as the leaf does via cmd.Flags().Changed) must survive into the
// request body, not be dropped by omitempty.
func TestRun_explicitZeroFlags_reachTheBody(t *testing.T) {
	var gotBody string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		b, _ := readAll(r)
		gotBody = b
		return gamescmdtest.JSONResp(`{"id":"z"}`), nil
	}))
	_, err := createcmd.Run(rc, createcmd.Input{
		ApplicationID: "999",
		Write: gamescmd.AchievementWrite{
			Name:          "Zero",
			Type:          "INCREMENTAL",
			PointValue:    0,
			PointValueSet: true,
			StepsToUnlock: 0,
			StepsSet:      true,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gotBody, `"pointValue":0`) {
		t.Errorf("body %q should carry pointValue:0", gotBody)
	}
	if !strings.Contains(gotBody, `"stepsToUnlock":0`) {
		t.Errorf("body %q should carry stepsToUnlock:0", gotBody)
	}
}

func readAll(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.String(), err
}
