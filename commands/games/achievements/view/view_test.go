package view_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/games/achievements/view"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
)

const viewBody = `{"id":"a7","achievementType":"INCREMENTAL","draft":{"name":{"translations":[{"locale":"en-US","value":"Climber"}]},"pointValue":25}}`

func TestRun_happyPath_addressesIDAndPassesThrough(t *testing.T) {
	var gotURL string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL = r.URL.String()
		return gamescmdtest.JSONResp(viewBody), nil
	}))
	r, err := viewcmd.Run(rc, viewcmd.Input{ID: "a7"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(gotURL, "/achievements/a7") {
		t.Errorf("url %q should address achievement a7", gotURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != viewBody {
		t.Errorf("json = %s, want verbatim", out.String())
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
	_, err := viewcmd.Run(rc, viewcmd.Input{})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
