// Package add_test exercises `gplay team users add` at the command level: the
// --dry-run preview + `requires` array, the --role XOR --permissions guard, the
// exit-3 admin gate, the routine (ungated) happy path, and CI=true never
// auto-confirming the gate.
package add_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	addcmd "github.com/PollyGlot/google-play-cli/commands/team/users/add"
	"github.com/PollyGlot/google-play-cli/internal/exit"
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

// preview renders the dry-run JSON of a Renderable into a generic map.
func preview(t *testing.T, rc *kernel.RunContext, in addcmd.Input) (map[string]any, error) {
	t.Helper()
	r, err := addcmd.Run(rc, in)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatalf("dry-run JSON not parseable: %v\n%s", err, b.String())
	}
	return m, nil
}

// TestAdd_dryRun_role_matchesInput asserts a dry-run resolves --role to the
// bundle's account enums and reports no required safety flags for a routine add.
func TestAdd_dryRun_role_matchesInput(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	m, err := preview(t, rc, addcmd.Input{Email: "bob@example.com", Role: "reviewer", RoleSet: true, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if m["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", m["dryRun"])
	}
	perms := toStrings(m["permissions"])
	want := []string{"CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", "CAN_VIEW_APP_QUALITY_GLOBAL", "CAN_REPLY_TO_REVIEWS_GLOBAL"}
	if strings.Join(perms, ",") != strings.Join(want, ",") {
		t.Errorf("permissions = %v, want %v", perms, want)
	}
	if reqs := toStrings(m["requires"]); len(reqs) != 0 {
		t.Errorf("routine add requires = %v, want none", reqs)
	}
	if rt.Wrote() {
		t.Errorf("dry-run must make no write; calls=%v", rt.Calls())
	}
}

// TestAdd_dryRun_admin_requiresGrantAdmin asserts a dry-run of an admin add
// surfaces requires:["grant-admin"] but still previews (never refuses).
func TestAdd_dryRun_admin_requiresGrantAdmin(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	m, err := preview(t, rc, addcmd.Input{Email: "ada@example.com", Role: "admin", RoleSet: true, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run must preview even without --grant-admin: %v", err)
	}
	if got := toStrings(m["requires"]); strings.Join(got, ",") != "grant-admin" {
		t.Errorf("requires = %v, want [grant-admin]", got)
	}
}

// TestAdd_live_routine_creates asserts a routine add issues users.create with
// the resolved enums and no gate.
func TestAdd_live_routine_creates(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("POST", "/users", 200, `{"email":"bob@example.com"}`))
	rc := teamtest.NewRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Email: "bob@example.com", Permissions: []string{"reply-reviews"}, PermsSet: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	calls := rt.Calls()
	if len(calls) != 1 || calls[0].Method != "POST" || !strings.HasSuffix(calls[0].Path, "/users") {
		t.Fatalf("expected one POST .../users; calls=%v", calls)
	}
	if !strings.Contains(string(calls[0].Body), "CAN_REPLY_TO_REVIEWS_GLOBAL") {
		t.Errorf("create body should carry the resolved account enum; body=%s", calls[0].Body)
	}
}

// TestAdd_admin_withoutGrantAdmin_exit3 asserts a live admin add refuses with
// exit 3 (naming --grant-admin) and makes no write. CI=true must not change it.
func TestAdd_admin_withoutGrantAdmin_exit3(t *testing.T) {
	t.Setenv("CI", "true")
	rt := teamtest.New(teamtest.Fail("POST", "/users", 200, `{}`))
	rc := teamtest.NewRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Email: "ada@example.com", Role: "admin", RoleSet: true})
	if got := exitCodeOf(t, err); got != 3 {
		t.Fatalf("exit = %d, want 3; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "grant-admin") {
		t.Errorf("refusal %q should name --grant-admin", err.Error())
	}
	if rt.Wrote() {
		t.Errorf("a refused admin add must not write; calls=%v", rt.Calls())
	}
	// The error carries the structured Flag for agents to branch on.
	var sfe *exit.SafetyFlagError
	if !errors.As(err, &sfe) || sfe.Flag != "grant-admin" {
		t.Errorf("err should be a *exit.SafetyFlagError{Flag:\"grant-admin\"}, got %v", err)
	}
}

// TestAdd_admin_withGrantAdmin_dryRun_previewsOnly asserts --grant-admin
// --dry-run previews and makes no write.
func TestAdd_admin_withGrantAdmin_dryRun_previewsOnly(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Email: "ada@example.com", Role: "admin", RoleSet: true, GrantAdmin: true, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if rt.Wrote() {
		t.Errorf("dry-run must make no write; calls=%v", rt.Calls())
	}
}

// TestAdd_roleAndPermissions_exit2 / bare add → exit 2.
func TestAdd_flagMisuse_exit2(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	if _, err := addcmd.Run(rc, addcmd.Input{Email: "x@example.com", Role: "viewer", RoleSet: true, Permissions: []string{"reply-reviews"}, PermsSet: true}); exitCodeOf(t, err) != 2 {
		t.Errorf("--role AND --permissions should be exit 2")
	}
	if _, err := addcmd.Run(rc, addcmd.Input{Email: "x@example.com"}); exitCodeOf(t, err) != 2 {
		t.Errorf("bare add (no role/permissions) should be exit 2")
	}
	if rt.Wrote() {
		t.Errorf("misuse must make no write; calls=%v", rt.Calls())
	}
}

// TestAdd_forbidden_exit11 maps a users.create 403 to exit 11 with a hint.
func TestAdd_forbidden_exit11(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("POST", "/users", 403, `{"error":{"message":"nope"}}`))
	rc := teamtest.NewRC(t, rt)

	_, err := addcmd.Run(rc, addcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true})
	if got := exitCodeOf(t, err); got != 11 {
		t.Errorf("exit = %d, want 11; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "Users & permissions") {
		t.Errorf("403 hint should point at Users & permissions; got %q", err.Error())
	}
}

// TestNewCommand_flags asserts the flag/name surface.
func TestNewCommand_flags(t *testing.T) {
	cmd := addcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"role", "permissions", "grant-admin", "dry-run", "developer-id", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
