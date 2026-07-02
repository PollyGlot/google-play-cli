package delete_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	deletecmd "github.com/PollyGlot/google-play-cli/commands/games/achievements/delete"
	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd/gamescmdtest"
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
	_, err := deletecmd.Run(rc, deletecmd.Input{ID: "a7"})
	var sfe interface {
		ExitCode() int
		Error() string
	}
	if !errors.As(err, &sfe) || sfe.ExitCode() != 3 {
		t.Fatalf("err = %v, want exit 3 (missing --confirm)", err)
	}
	if !strings.Contains(err.Error(), "irreversible") {
		t.Errorf("error %q should explain the gate", err)
	}
}

func TestRun_dryRun_reportsGateAndDoesNotCall(t *testing.T) {
	rc := gamescmdtest.NewRC(t, noCall(t))
	r, err := deletecmd.Run(rc, deletecmd.Input{ID: "a7", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"dryRun"`) || !strings.Contains(out.String(), `"confirm"`) {
		t.Errorf("dry-run json = %s, want dryRun + requires confirm", out.String())
	}
}

func TestRun_happyPath_deletesAndConfirms(t *testing.T) {
	var gotURL, gotMethod string
	var stderr bytes.Buffer
	rc := gamescmdtest.NewRC(t, gamescmdtest.RTFunc(func(r *http.Request) (*http.Response, error) {
		if resp, ok := gamescmdtest.Token(r); ok {
			return resp, nil
		}
		gotURL, gotMethod = r.URL.String(), r.Method
		return gamescmdtest.StatusResp(204, ""), nil
	}))
	rc.Stderr = &stderr
	r, err := deletecmd.Run(rc, deletecmd.Input{ID: "a7", Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodDelete || !strings.HasSuffix(gotURL, "/achievements/a7") {
		t.Errorf("delete should DELETE /achievements/a7, got %s %s", gotMethod, gotURL)
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"deleted"`) || !strings.Contains(out.String(), "true") {
		t.Errorf("json = %s, want deleted:true", out.String())
	}
	if !strings.Contains(stderr.String(), "✓") {
		t.Errorf("stderr = %q, want a ✓ confirmation line", stderr.String())
	}
}
