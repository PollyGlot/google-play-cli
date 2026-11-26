package set_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	setcmd "github.com/PollyGlot/google-play-cli/commands/team/users/set"
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

func previewMap(t *testing.T, rc *kernel.RunContext, in setcmd.Input) map[string]any {
	t.Helper()
	r, err := setcmd.Run(rc, in)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatalf("dry-run JSON not parseable: %v\n%s", err, b.String())
	}
	return m
}

func TestSet_live_replaces(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("PATCH", "/users/bob@example.com", 200, `{"email":"bob@example.com"}`))
	rc := teamtest.NewRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Role: "reviewer", RoleSet: true})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	calls := rt.Calls()
	if len(calls) != 1 || calls[0].Method != "PATCH" {
		t.Fatalf("expected one PATCH; calls=%v", calls)
	}
	if !strings.Contains(calls[0].Query, "updateMask=developerAccountPermissions") {
		t.Errorf("PATCH should carry the updateMask; query=%q", calls[0].Query)
	}
	if !strings.Contains(string(calls[0].Body), "CAN_REPLY_TO_REVIEWS_GLOBAL") {
		t.Errorf("patch body should carry the resolved enums; body=%s", calls[0].Body)
	}
}

func TestSet_bare_exit2(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com"})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2 (bare set); err=%v", got, err)
	}
	if rt.Wrote() {
		t.Errorf("a refused set must not write; calls=%v", rt.Calls())
	}
}

func TestSet_clear_empties(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("PATCH", "/users/bob@example.com", 200, `{"email":"bob@example.com"}`))
	rc := teamtest.NewRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Clear: true})
	if err != nil {
		t.Fatalf("set --clear: %v", err)
	}
	body := string(rt.Calls()[0].Body)
	if !strings.Contains(body, `"developerAccountPermissions":[]`) {
		t.Errorf("--clear should send an explicit empty array; body=%s", body)
	}
}

func TestSet_reducing_isUngated(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	m := previewMap(t, rc, setcmd.Input{Email: "bob@example.com", Permissions: []string{"view-app-info"}, PermsSet: true, DryRun: true})
	if reqs, _ := m["requires"].([]any); len(reqs) != 0 {
		t.Errorf("a reducing set must require no safety flag, got %v", reqs)
	}
}

func TestSet_admin_withoutGrantAdmin_exit3(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("PATCH", "/users/bob@example.com", 200, `{}`))
	rc := teamtest.NewRC(t, rt)
	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Permissions: []string{"manage-permissions"}, PermsSet: true})
	if got := exitCodeOf(t, err); got != 3 {
		t.Errorf("exit = %d, want 3; err=%v", got, err)
	}
	if rt.Wrote() {
		t.Errorf("a refused admin set must not write; calls=%v", rt.Calls())
	}
}

func TestSet_notMember_exit30(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("PATCH", "/users/ghost@example.com", 404, `{"error":{"message":"not found"}}`))
	rc := teamtest.NewRC(t, rt)
	_, err := setcmd.Run(rc, setcmd.Input{Email: "ghost@example.com", Role: "viewer", RoleSet: true})
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("exit = %d, want 30; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "team users add") {
		t.Errorf("404 hint should point at `team users add`; got %q", err.Error())
	}
}

func TestNewCommand_flags(t *testing.T) {
	cmd := setcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"role", "permissions", "clear", "grant-admin", "dry-run", "developer-id", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}

func TestSet_blankSelector_exit2PointsToClear(t *testing.T) {
	cases := map[string]setcmd.Input{
		`--permissions ""`:       {Permissions: []string{}, PermsSet: true},
		`--permissions " , "`:    {Permissions: []string{" ", " "}, PermsSet: true},
		`--permissions "\u00a0"`: {Permissions: []string{"\u00a0"}, PermsSet: true},
		`--role ""`:              {Role: "", RoleSet: true},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			rt := teamtest.New()
			rc := teamtest.NewRC(t, rt)
			in.Email = "bob@example.com"
			in.DryRun = true
			_, err := setcmd.Run(rc, in)
			if got := exitCodeOf(t, err); got != 2 {
				t.Fatalf("exit = %d, want 2; err=%v", got, err)
			}
			if !strings.Contains(err.Error(), "--clear") {
				t.Errorf("message should point to --clear; got %q", err.Error())
			}
			if calls := rt.Calls(); len(calls) != 0 {
				t.Errorf("a blank selector must reach no HTTP; calls=%v", calls)
			}
		})
	}
}

func TestSet_empty2xxBody_exit30(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("PATCH", "/users/bob@example.com", 200, ""))
	rc := teamtest.NewRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Role: "reviewer", RoleSet: true})
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("exit = %d, want 30 (undecodable 2xx body); err=%v", got, err)
	}
}
