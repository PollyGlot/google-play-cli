package delete_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
	deletecmd "github.com/PollyGlot/google-play-cli/commands/games/leaderboards/delete"
)

func noCall(t *testing.T) gamescmdtest.RTFunc {
	return func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		t.Fatalf("unexpected API call: %s", r.URL)
		return nil, nil
	}
}

func TestRun_missingConfirm_exit3(t *testing.T) {
	rc := gamescmdtest.NewRC(t, noCall(t))
	_, err := deletecmd.Run(rc, deletecmd.Input{ID: "l9"})
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != 3 {
		t.Fatalf("err = %v, want exit 3 (missing --confirm)", err)
	}
}

func TestRun_dryRun_doesNotCall(t *testing.T) {
	rc := gamescmdtest.NewRC(t, noCall(t))
	r, err := deletecmd.Run(rc, deletecmd.Input{ID: "l9", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"confirm"`) {
		t.Errorf("dry-run json = %s, want requires confirm", out.String())
	}
}

func TestRun_happyPath_deletes(t *testing.T) {
	var gotURL, gotMethod string
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL, gotMethod = r.URL.String(), r.Method
		return gamescmdtest.StatusResp(204, ""), nil
	}))
	if _, err := deletecmd.Run(rc, deletecmd.Input{ID: "l9", Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodDelete || !strings.HasSuffix(gotURL, "/leaderboards/l9") {
		t.Errorf("delete should DELETE /leaderboards/l9, got %s %s", gotMethod, gotURL)
	}
}
