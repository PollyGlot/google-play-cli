package update_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	updatecmd "github.com/PollyGlot/google-play-cli/commands/games/achievements/update"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
)

func TestRun_happyPath_putsToResource(t *testing.T) {
	var gotURL, gotMethod, gotBody string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL, gotMethod = r.URL.String(), r.Method
		var b bytes.Buffer
		if r.Body != nil {
			_, _ = b.ReadFrom(r.Body)
		}
		gotBody = b.String()
		return gamescmdtest.JSONResp(`{"id":"a7"}`), nil
	}))
	_, err := updatecmd.Run(rc, updatecmd.Input{ID: "a7", Write: gamescmd.AchievementWrite{StepsToUnlock: 5, StepsSet: true}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotURL, "/achievements/a7") {
		t.Errorf("url %q should address achievement a7", gotURL)
	}
	if !strings.Contains(gotBody, `"stepsToUnlock":5`) {
		t.Errorf("body %q should carry the field", gotBody)
	}
}

// TestRun_explicitZeroFlag_reachesBody guards that an explicit --point-value 0
// survives into the PUT body on update (the *int fix), not dropped by omitempty.
func TestRun_explicitZeroFlag_reachesBody(t *testing.T) {
	var gotBody string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		var b bytes.Buffer
		if r.Body != nil {
			_, _ = b.ReadFrom(r.Body)
		}
		gotBody = b.String()
		return gamescmdtest.JSONResp(`{"id":"a7"}`), nil
	}))
	_, err := updatecmd.Run(rc, updatecmd.Input{ID: "a7", Write: gamescmd.AchievementWrite{PointValue: 0, PointValueSet: true}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gotBody, `"pointValue":0`) {
		t.Errorf("body %q should carry pointValue:0", gotBody)
	}
}

func TestRun_missingID_exit2(t *testing.T) {
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		t.Fatalf("unexpected call: %s", r.URL)
		return nil, nil
	}))
	_, err := updatecmd.Run(rc, updatecmd.Input{Write: gamescmd.AchievementWrite{Name: "x"}})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
