package list_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	listcmd "github.com/PollyGlot/google-play-cli/commands/games/achievements/list"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
)

const listBody = `{"kind":"x","items":[{"id":"a1","achievementType":"STANDARD","draft":{"name":{"translations":[{"locale":"en-US","value":"Win"}]},"pointValue":10}}],"nextPageToken":"tok"}`

func TestRun_happyPath_addressesApplicationAndPassesThrough(t *testing.T) {
	var gotURL string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL = r.URL.String()
		return gamescmdtest.JSONResp(listBody), nil
	}))
	r, err := listcmd.Run(rc, listcmd.Input{ApplicationID: "12345", MaxResults: 50})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(gotURL, "/applications/12345/achievements") {
		t.Errorf("url %q should address the application's achievements", gotURL)
	}
	if !strings.Contains(gotURL, "maxResults=50") {
		t.Errorf("url %q should carry --max-results", gotURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.TrimSpace(out.String()) != listBody {
		t.Errorf("json = %s, want verbatim", out.String())
	}
}

func TestRun_missingApplicationID_exit2(t *testing.T) {
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		t.Fatalf("unexpected call: %s", r.URL)
		return nil, nil
	}))
	_, err := listcmd.Run(rc, listcmd.Input{})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 2 {
		t.Errorf("err = %v, want exit 2", err)
	}
}
