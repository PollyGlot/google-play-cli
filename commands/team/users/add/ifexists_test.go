package add_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	addcmd "github.com/PollyGlot/google-play-cli/commands/team/users/add"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/ifexists"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/teamtest"
)

// existingMember is the users.list object of a member who is already in the
// account, with a field gplay does not model so a verbatim pass-through is
// observable. The address differs in case from the one passed to add: emails
// match case-insensitively.
const existingMember = `{"name":"developers/4900000000000000000/users/Bob@Example.com","email":"Bob@Example.com","accessState":"ACCESS_GRANTED","developerAccountPermissions":["CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL"],"unmodeled":true}`

const (
	usersWithBob = `{"users":[{"email":"other@example.com"},` + existingMember + `]}`
	usersEmpty   = `{"users":[{"email":"other@example.com"}]}`
	patchResp    = `{"email":"Bob@Example.com","developerAccountPermissions":["CAN_REPLY_TO_REVIEWS_GLOBAL"],"patched":true}`
	createResp   = `{"email":"bob@example.com","developerAccountPermissions":["CAN_REPLY_TO_REVIEWS_GLOBAL"]}`
)

// teamAPI serves the --if-exists flow: users.list (the lookup), users.create
// and users.patch. Anything else is unclaimed (200, empty) and shows in calls.
func teamAPI(list string) *teamtest.RT {
	return teamtest.New(
		teamtest.UsersList(list),
		teamtest.Fail("POST", "/users", 200, createResp),
		teamtest.Fail("PATCH", "/users/bob@example.com", 200, patchResp),
	)
}

func methods(rt *teamtest.RT) []string {
	var out []string
	for _, c := range rt.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// run invokes add and renders its JSON (stdout under --output json), with
// stderr captured.
func run(t *testing.T, rc *kernel.RunContext, in addcmd.Input) (stdout, stderr string, err error) {
	t.Helper()
	var se bytes.Buffer
	rc.Stderr = &se
	r, err := addcmd.Run(rc, in)
	if err != nil {
		return "", se.String(), err
	}
	var so bytes.Buffer
	if err := r.Renderers().JSON(&so); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	return so.String(), se.String(), nil
}

const usersPath = "/androidpublisher/v3/developers/4900000000000000000/users"

// Existing member + skip: one lookup, no write, exit 0, stdout is the matched
// users.list object verbatim, stderr says it was skipped without a ✓.
func TestAdd_ifExistsSkip_existingMember_writesNothing(t *testing.T) {
	rt := teamAPI(usersWithBob)
	rc := teamtest.NewRC(t, rt)

	stdout, stderr, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "release-manager", RoleSet: true, IfExists: ifexists.Skip})
	if err != nil {
		t.Fatalf("add --if-exists skip: %v", err)
	}
	if got := methods(rt); strings.Join(got, ",") != "GET "+usersPath {
		t.Errorf("calls = %v, want the users.list lookup only", got)
	}
	if stdout != existingMember {
		t.Errorf("stdout = %s\nwant the matched user verbatim: %s", stdout, existingMember)
	}
	if !strings.Contains(stderr, "already exists, skipped") || strings.Contains(stderr, "✓") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("stderr = %q, want one \"already exists, skipped\" line and no ✓", stderr)
	}
}

// Absent member + skip or update: the lookup finds nobody, then the invite
// runs exactly as without the flag (users.create, its response on stdout).
func TestAdd_ifExists_absentMember_looksUpThenCreates(t *testing.T) {
	for _, policy := range []ifexists.Policy{ifexists.Skip, ifexists.Update} {
		t.Run(string(policy), func(t *testing.T) {
			rt := teamAPI(usersEmpty)
			rc := teamtest.NewRC(t, rt)

			stdout, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Permissions: []string{"reply-reviews"}, PermsSet: true, IfExists: policy})
			if err != nil {
				t.Fatalf("add --if-exists %s: %v", policy, err)
			}
			if got := strings.Join(methods(rt), ","); got != "GET "+usersPath+",POST "+usersPath {
				t.Errorf("calls = %s, want the lookup then users.create", got)
			}
			if stdout != createResp {
				t.Errorf("stdout = %s, want the users.create response %s", stdout, createResp)
			}
		})
	}
}

// Existing member + update: the `team users set` write (users.patch with the
// resolved permissions, replace semantics), its response on stdout, and no
// users.create.
func TestAdd_ifExistsUpdate_existingMember_patches(t *testing.T) {
	rt := teamAPI(usersWithBob)
	rc := teamtest.NewRC(t, rt)

	stdout, stderr, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Permissions: []string{"reply-reviews"}, PermsSet: true, IfExists: ifexists.Update})
	if err != nil {
		t.Fatalf("add --if-exists update: %v", err)
	}
	calls := rt.Calls()
	if got := strings.Join(methods(rt), ","); got != "GET "+usersPath+",PATCH "+usersPath+"/bob@example.com" {
		t.Fatalf("calls = %s, want the lookup then users.patch", got)
	}
	patch := calls[1]
	if patch.Query != "updateMask=developerAccountPermissions" {
		t.Errorf("query = %q, want updateMask=developerAccountPermissions", patch.Query)
	}
	if string(patch.Body) != `{"developerAccountPermissions":["CAN_REPLY_TO_REVIEWS_GLOBAL"]}` {
		t.Errorf("patch body = %s, want the resolved set as a full replacement", patch.Body)
	}
	if stdout != patchResp {
		t.Errorf("stdout = %s, want the users.patch response %s", stdout, patchResp)
	}
	if !strings.Contains(stderr, "already exists, updated") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("stderr = %q, want one \"already exists, updated\" line", stderr)
	}
}

// Conferring admin without --grant-admin is refused (exit 3, the structured
// grant-admin flag) before the lookup, under every policy: the gate of the
// invite and of `team users set` cannot be bypassed by the idempotent path.
func TestAdd_ifExists_adminWithoutGrantAdmin_exit3BeforeLookup(t *testing.T) {
	for _, policy := range []ifexists.Policy{ifexists.Skip, ifexists.Update} {
		t.Run(string(policy), func(t *testing.T) {
			rt := teamAPI(usersWithBob)
			rc := teamtest.NewRC(t, rt)

			_, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "admin", RoleSet: true, IfExists: policy})
			if got := exitCodeOf(t, err); got != 3 {
				t.Fatalf("exit = %d, want 3; err=%v", got, err)
			}
			var sfe *exit.SafetyFlagError
			if !errors.As(err, &sfe) || sfe.Flag != "grant-admin" {
				t.Errorf("err = %v, want a SafetyFlagError naming grant-admin", err)
			}
			if calls := rt.Calls(); len(calls) != 0 {
				t.Errorf("a refused admin add must not even look up; calls=%v", methods(rt))
			}
		})
	}
}

// With --grant-admin, update confers admin through the same patch.
func TestAdd_ifExistsUpdate_adminWithGrantAdmin_patches(t *testing.T) {
	rt := teamAPI(usersWithBob)
	rc := teamtest.NewRC(t, rt)

	if _, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "admin", RoleSet: true, GrantAdmin: true, IfExists: ifexists.Update}); err != nil {
		t.Fatalf("add --if-exists update --grant-admin: %v", err)
	}
	calls := rt.Calls()
	if len(calls) != 2 || calls[1].Method != "PATCH" || !strings.Contains(string(calls[1].Body), "CAN_MANAGE_PERMISSIONS_GLOBAL") {
		t.Errorf("want the lookup then a patch conferring admin; calls=%v", methods(rt))
	}
}

// The --dry-run preview of update names the gate the live write needs, the
// way `team users set --dry-run` does.
func TestAdd_ifExistsUpdate_dryRunAdmin_requiresGrantAdmin(t *testing.T) {
	rt := teamAPI(usersWithBob)
	rc := teamtest.NewRC(t, rt)

	m, err := preview(t, rc, addcmd.Input{Email: "bob@example.com", Role: "admin", RoleSet: true, IfExists: ifexists.Update, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run must preview even without --grant-admin: %v", err)
	}
	if got := toStrings(m["requires"]); strings.Join(got, ",") != "grant-admin" {
		t.Errorf("requires = %v, want [grant-admin]", got)
	}
}

// Default and explicit fail: no lookup, users.create as before, and a 409
// keeps its exit code (60) and its `team users set` hint.
func TestAdd_ifExistsFail_noLookup_conflictUnchanged(t *testing.T) {
	for _, policy := range []ifexists.Policy{"", ifexists.Fail} {
		t.Run("policy="+string(policy), func(t *testing.T) {
			rt := teamtest.New(
				teamtest.UsersList(usersWithBob),
				teamtest.Fail("POST", "/users", 409, `{"error":{"code":409,"message":"already exists","status":"ALREADY_EXISTS"}}`),
			)
			rc := teamtest.NewRC(t, rt)

			_, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true, IfExists: policy})
			if got := exitCodeOf(t, err); got != 60 {
				t.Errorf("exit = %d, want 60; err=%v", got, err)
			}
			if !strings.Contains(err.Error(), "already a member: use `gplay team users set bob@example.com`") {
				t.Errorf("409 hint changed: %q", err.Error())
			}
			if got := strings.Join(methods(rt), ","); got != "POST "+usersPath {
				t.Errorf("calls = %s, want users.create only (fail adds no lookup)", got)
			}
		})
	}
}

// --dry-run stays offline under every policy and reports a non-default one;
// the default preview keeps its bytes (no ifExists key).
func TestAdd_ifExists_dryRunOfflineAndReportsPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy ifexists.Policy
		want   any
	}{{ifexists.Skip, "skip"}, {ifexists.Update, "update"}, {ifexists.Fail, nil}, {"", nil}} {
		rt := teamAPI(usersWithBob)
		rc := teamtest.NewRC(t, rt)

		m, err := preview(t, rc, addcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true, IfExists: tc.policy, DryRun: true})
		if err != nil {
			t.Fatalf("dry-run %q: %v", tc.policy, err)
		}
		if m["ifExists"] != tc.want {
			t.Errorf("policy %q: ifExists = %v, want %v", tc.policy, m["ifExists"], tc.want)
		}
		if calls := rt.Calls(); len(calls) != 0 {
			t.Errorf("policy %q: dry-run made requests %v", tc.policy, methods(rt))
		}
	}
}

// An unknown policy is CLI misuse (exit 2) before any HTTP, for a direct
// caller of Run and at flag parsing.
func TestAdd_ifExists_invalidValue_exit2(t *testing.T) {
	rt := teamAPI(usersWithBob)
	rc := teamtest.NewRC(t, rt)
	_, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true, IfExists: "bogus"})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("Run exit = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "skip, update or fail") {
		t.Errorf("error %q should name the allowed values", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("misuse made requests %v", methods(rt))
	}

	cmd := addcmd.NewCommand(kernel.Boot{})
	if err := cmd.Flags().Set(ifexists.Name, "bogus"); err == nil || exit.For(err) != 2 {
		t.Errorf("flag parse of bogus: err=%v, want exit 2", err)
	}
	if got := cmd.Flags().Lookup(ifexists.Name).Value.Type(); got != "skip|update|fail" {
		t.Errorf("flag type = %q, want skip|update|fail", got)
	}
}

// A lookup that fails (here a 403) stops before any write, with the add hint.
func TestAdd_ifExists_lookupFails_noWrite(t *testing.T) {
	rt := teamtest.New(
		teamtest.Fail("GET", "/users", 403, `{"error":{"code":403,"message":"nope"}}`),
		teamtest.Fail("POST", "/users", 200, createResp),
	)
	rc := teamtest.NewRC(t, rt)

	_, _, err := run(t, rc, addcmd.Input{Email: "bob@example.com", Role: "viewer", RoleSet: true, IfExists: ifexists.Skip})
	if got := exitCodeOf(t, err); got != 11 {
		t.Errorf("exit = %d, want 11; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "Users & permissions") {
		t.Errorf("403 hint missing: %q", err)
	}
	if rt.Wrote() {
		t.Errorf("a failed lookup must not write; calls=%v", methods(rt))
	}
}
