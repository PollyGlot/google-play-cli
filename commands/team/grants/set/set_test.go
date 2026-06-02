// Package set_test exercises `gplay team grants set` at the command level: the
// read-then-decide upsert (create vs update via a stubbed current state), the
// dry-run verb + diff (read, no write), app-scope resolution, and the gates.
package set_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	setcmd "github.com/PollyGlot/google-play-cli/commands/team/grants/set"
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

// bobNoGrant / bobWithGrant are stubbed users.list responses for the upsert's
// read-then-decide.
const (
	bobNoGrant   = `{"users":[{"email":"bob@example.com"}]}`
	bobWithGrant = `{"users":[{"email":"bob@example.com","grants":[{"packageName":"com.example.foo","appLevelPermissions":["CAN_VIEW_APP_QUALITY"]}]}]}`
)

func dryRun(t *testing.T, rc *kernel.RunContext, in setcmd.Input) map[string]any {
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

// TestGrantsSet_create asserts an absent grant is created via grants.create
// with bare app-level enums (app scope strips _GLOBAL).
func TestGrantsSet_create(t *testing.T) {
	rt := teamtest.New(
		teamtest.UsersList(bobNoGrant),
		teamtest.Fail("POST", "/grants", 200, `{"packageName":"com.example.foo"}`),
	)
	rc := teamtest.NewRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Permissions: []string{"reply-reviews"}, PermsSet: true})
	if err != nil {
		t.Fatalf("grants set (create): %v", err)
	}
	post, ok := findCall(rt.Calls(), "POST")
	if !ok {
		t.Fatalf("expected a POST .../grants; calls=%v", rt.Calls())
	}
	if !strings.Contains(string(post.Body), `"CAN_REPLY_TO_REVIEWS"`) || strings.Contains(string(post.Body), "_GLOBAL") {
		t.Errorf("create body should carry the bare app enum; body=%s", post.Body)
	}
}

// TestGrantsSet_update asserts a present grant is updated via grants.patch with
// the updateMask.
func TestGrantsSet_update(t *testing.T) {
	rt := teamtest.New(
		teamtest.UsersList(bobWithGrant),
		teamtest.Fail("PATCH", "/grants/com.example.foo", 200, `{"packageName":"com.example.foo"}`),
	)
	rc := teamtest.NewRC(t, rt)

	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Permissions: []string{"reply-reviews"}, PermsSet: true})
	if err != nil {
		t.Fatalf("grants set (update): %v", err)
	}
	patch, ok := findCall(rt.Calls(), "PATCH")
	if !ok {
		t.Fatalf("expected a PATCH .../grants/com.example.foo; calls=%v", rt.Calls())
	}
	if !strings.Contains(patch.Query, "updateMask=appLevelPermissions") {
		t.Errorf("patch should carry the updateMask; query=%q", patch.Query)
	}
}

// findCall returns the first captured call with the given method.
func findCall(calls []teamtest.Call, method string) (teamtest.Call, bool) {
	for _, c := range calls {
		if c.Method == method {
			return c, true
		}
	}
	return teamtest.Call{}, false
}

// TestGrantsSet_dryRun_update_verbAndDiff asserts the dry-run reads the current
// state and reports verb=update + the diff, making NO write.
func TestGrantsSet_dryRun_update_verbAndDiff(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(bobWithGrant))
	rc := teamtest.NewRC(t, rt)

	m := dryRun(t, rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Permissions: []string{"reply-reviews"}, PermsSet: true, DryRun: true})
	if m["verb"] != "update" {
		t.Errorf("verb = %v, want update", m["verb"])
	}
	if cur := toStrings(m["current"]); strings.Join(cur, ",") != "CAN_VIEW_APP_QUALITY" {
		t.Errorf("current = %v, want [CAN_VIEW_APP_QUALITY]", cur)
	}
	if des := toStrings(m["desired"]); strings.Join(des, ",") != "CAN_REPLY_TO_REVIEWS" {
		t.Errorf("desired = %v, want [CAN_REPLY_TO_REVIEWS]", des)
	}
	if add := toStrings(m["add"]); strings.Join(add, ",") != "CAN_REPLY_TO_REVIEWS" {
		t.Errorf("add = %v, want [CAN_REPLY_TO_REVIEWS]", add)
	}
	if rm := toStrings(m["remove"]); strings.Join(rm, ",") != "CAN_VIEW_APP_QUALITY" {
		t.Errorf("remove = %v, want [CAN_VIEW_APP_QUALITY]", rm)
	}
	if rt.Wrote() {
		t.Errorf("dry-run must make no write; calls=%v", rt.Calls())
	}
	// It DID read (the read powers the exact verb + diff).
	if len(rt.Calls()) == 0 {
		t.Errorf("dry-run should perform the idempotent read")
	}
}

// TestGrantsSet_dryRun_create_verb asserts an absent grant dry-runs as
// verb=create with an empty current set.
func TestGrantsSet_dryRun_create_verb(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(bobNoGrant))
	rc := teamtest.NewRC(t, rt)
	m := dryRun(t, rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Role: "reviewer", RoleSet: true, DryRun: true})
	if m["verb"] != "create" {
		t.Errorf("verb = %v, want create", m["verb"])
	}
	if cur := toStrings(m["current"]); len(cur) != 0 {
		t.Errorf("current = %v, want empty for a create", cur)
	}
}

// TestGrantsSet_admin_withoutGrantAdmin_exit3 asserts an admin-conferring grant
// is gated before any network.
func TestGrantsSet_admin_withoutGrantAdmin_exit3(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(bobWithGrant))
	rc := teamtest.NewRC(t, rt)
	_, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Permissions: []string{"manage-permissions"}, PermsSet: true})
	if got := exitCodeOf(t, err); got != 3 {
		t.Fatalf("exit = %d, want 3; err=%v", got, err)
	}
	if rt.Wrote() {
		t.Errorf("a refused admin grant must not write; calls=%v", rt.Calls())
	}
}

// TestGrantsSet_notMember_exit30 asserts granting to a non-member fails with
// exit 30 and points at `team users add`.
func TestGrantsSet_notMember_exit30(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(`{"users":[{"email":"someoneelse@example.com"}]}`))
	rc := teamtest.NewRC(t, rt)
	_, err := setcmd.Run(rc, setcmd.Input{Email: "ghost@example.com", Package: "com.example.foo", Role: "viewer", RoleSet: true})
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("exit = %d, want 30; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "team users add") {
		t.Errorf("not-member error should point at `team users add`; got %q", err.Error())
	}
}

// TestGrantsSet_misuse_exit2 covers the missing-package and role/permissions
// conflict.
func TestGrantsSet_misuse_exit2(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	if _, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true}); exitCodeOf(t, err) != 2 {
		t.Errorf("missing --package should be exit 2")
	}
	if _, err := setcmd.Run(rc, setcmd.Input{Email: "bob@example.com", Package: "com.example.foo", Role: "viewer", RoleSet: true, Permissions: []string{"x"}, PermsSet: true}); exitCodeOf(t, err) != 2 {
		t.Errorf("--role AND --permissions should be exit 2")
	}
	if rt.Wrote() {
		t.Errorf("misuse must make no write; calls=%v", rt.Calls())
	}
}

// TestNewCommand_flags asserts the flag/name surface.
func TestNewCommand_flags(t *testing.T) {
	cmd := setcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "role", "permissions", "grant-admin", "dry-run", "developer-id", "output"} {
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
