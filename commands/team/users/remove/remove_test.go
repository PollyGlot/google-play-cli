package remove_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	removecmd "github.com/PollyGlot/google-play-cli/commands/team/users/remove"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/teamtest"
)

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

func TestRemove_confirm_deletes(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("DELETE", "/users/bob@example.com", 204, ""))
	rc := teamtest.NewRC(t, rt)

	r, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Confirm: true})
	if err != nil {
		t.Fatalf("remove --confirm: %v", err)
	}
	calls := rt.Calls()
	if len(calls) != 1 || calls[0].Method != "DELETE" || !strings.HasSuffix(calls[0].Path, "/users/bob@example.com") {
		t.Fatalf("expected one DELETE .../users/bob@example.com; calls=%v", calls)
	}
	var b strings.Builder
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Removed string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(b.String()), &out); err != nil || !out.OK || out.Removed != "bob@example.com" {
		t.Errorf("success object = %s, err=%v", b.String(), err)
	}
}

func TestRemove_withoutConfirm_exit3(t *testing.T) {
	t.Setenv("CI", "true")
	rt := teamtest.New(teamtest.Fail("DELETE", "/users/bob@example.com", 204, ""))
	rc := teamtest.NewRC(t, rt)

	_, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com"})
	if got := exitCodeOf(t, err); got != 3 {
		t.Fatalf("exit = %d, want 3; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "--confirm") {
		t.Errorf("refusal %q should name --confirm", err.Error())
	}
	if rt.Wrote() {
		t.Errorf("a refused remove must not DELETE; calls=%v", rt.Calls())
	}
}

func TestRemove_dryRun_previews(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	r, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("dry-run must make no HTTP call; calls=%v", rt.Calls())
	}
	var b strings.Builder
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "confirm") {
		t.Errorf("dry-run JSON should report requires:[confirm]; got %s", b.String())
	}
}

func TestRemove_apiErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		wantExit int
	}{
		{"notFound", 404, 30},
		{"forbidden", 403, 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := teamtest.New(teamtest.Fail("DELETE", "/users/x@example.com", tc.status, `{"error":{"message":"nope"}}`))
			rc := teamtest.NewRC(t, rt)
			_, err := removecmd.Run(rc, removecmd.Input{Email: "x@example.com", Confirm: true})
			if got := exitCodeOf(t, err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d; err=%v", got, tc.wantExit, err)
			}
		})
	}
}

func TestNewCommand_flags(t *testing.T) {
	cmd := removecmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"confirm", "dry-run", "developer-id", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}

func TestRemove_emptyBody_printsAck_bodyPassesThrough(t *testing.T) {
	for body, want := range map[string]string{
		"":   "{\n  \"ok\": true,\n  \"removed\": \"bob@example.com\"\n}\n",
		"{}": "{}",
	} {
		rc := teamtest.NewRC(t, teamtest.New(teamtest.Fail("DELETE", "/users/bob@example.com", 200, body)))
		r, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Confirm: true})
		if err != nil {
			t.Fatalf("Run with body %q: %v", body, err)
		}
		var out strings.Builder
		if err := r.Renderers().JSON(&out); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if out.String() != want {
			t.Errorf("body %q: json = %q, want %q", body, out.String(), want)
		}
	}
}
