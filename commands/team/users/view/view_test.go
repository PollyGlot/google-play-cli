// Package view_test exercises `gplay team users view <email>` at the command
// level: it asserts external behaviour (the scalar header + grants in the
// table/markdown views, the verbatim single-User JSON pass-through, the
// case-insensitive match, and the error-code mapping incl. the synthetic
// member-not-found exit 30) against stubbed users.list responses via the shared
// teamtest transport. No network.
package view_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	viewcmd "github.com/PollyGlot/google-play-cli/commands/team/users/view"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/teamtest"
)

// aliceUser is the verbatim User object users.list returns for alice — an
// account-wide admin (CAN_MANAGE_PERMISSIONS_GLOBAL) with one app grant. The
// JSON view must echo these exact bytes (ADR-0003 pass-through).
const aliceUser = `{"email":"alice@example.com","accessState":"PENDING_SIGNUP","developerAccountPermissions":["CAN_MANAGE_PERMISSIONS_GLOBAL","CAN_VIEW_FINANCIAL_DATA_GLOBAL"],"grants":[{"packageName":"com.example.foo","appLevelPermissions":["CAN_VIEW_APP_QUALITY"]}]}`

// bobUser is a plain (non-admin) member with no grants.
const bobUser = `{"email":"bob@example.com","accessState":"INVITED"}`

func usersBody(users ...string) string {
	return `{"users":[` + strings.Join(users, ",") + `]}`
}

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

func renderTable(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().Table(&b); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	return b.String()
}

func renderJSON(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	return b.String()
}

func renderMarkdown(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().Markdown(&b); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	return b.String()
}

// run is the common path: stub a users.list body, resolve the member, render.
func run(t *testing.T, body, email string) (output.Renderable, error) {
	t.Helper()
	rt := teamtest.New(teamtest.UsersList(body))
	rc := teamtest.NewRC(t, rt)
	return viewcmd.Run(rc, viewcmd.Input{Email: email})
}

func TestTableShowsHeaderAndGrants(t *testing.T) {
	r, err := run(t, usersBody(aliceUser), "alice@example.com")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := renderTable(t, r)
	for _, want := range []string{
		"alice@example.com",
		"PENDING_SIGNUP",
		"PERMISSIONS",
		"CAN_MANAGE_PERMISSIONS_GLOBAL",
		"com.example.foo",
		"CAN_VIEW_APP_QUALITY",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n--- got ---\n%s", want, out)
		}
	}
	// Admin marker present for an all-permissions member.
	if !strings.Contains(out, "yes") {
		t.Errorf("admin member should be marked, got:\n%s", out)
	}
}

func TestJSONIsVerbatimSingleUser(t *testing.T) {
	r, err := run(t, usersBody(aliceUser, bobUser), "alice@example.com")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := strings.TrimSpace(renderJSON(t, r))
	if got != aliceUser {
		t.Errorf("JSON not verbatim single User\n want: %s\n  got: %s", aliceUser, got)
	}
}

func TestMarkdownRendersRecord(t *testing.T) {
	r, err := run(t, usersBody(aliceUser), "alice@example.com")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := renderMarkdown(t, r)
	for _, want := range []string{"## alice@example.com", "com.example.foo"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestMemberWithNoGrants(t *testing.T) {
	r, err := run(t, usersBody(bobUser), "bob@example.com")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := renderTable(t, r)
	if !strings.Contains(out, "bob@example.com") {
		t.Errorf("table missing member email:\n%s", out)
	}
	// A grant-less member must render without a spurious grant row.
	if strings.Contains(out, "com.example.foo") {
		t.Errorf("unexpected grant row for grant-less member:\n%s", out)
	}
}

func TestCaseInsensitiveMatch(t *testing.T) {
	r, err := run(t, usersBody(aliceUser), "Alice@Example.com")
	if err != nil {
		t.Fatalf("Run with mixed-case email: %v", err)
	}
	if !strings.Contains(renderTable(t, r), "alice@example.com") {
		t.Errorf("mixed-case email should resolve the stored member")
	}
}

func TestMemberNotFoundIsExit30(t *testing.T) {
	_, err := run(t, usersBody(bobUser), "ghost@example.com")
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("absent member exit = %d, want 30", got)
	}
	if !strings.Contains(err.Error(), "team users list") {
		t.Errorf("not-found error should hint at `gplay team users list`, got: %v", err)
	}
}

func TestForbiddenIsExit11(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("GET", "/users", 403, `{"error":{"code":403,"message":"forbidden"}}`))
	rc := teamtest.NewRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Email: "alice@example.com"})
	if got := exitCodeOf(t, err); got != 11 {
		t.Errorf("403 exit = %d, want 11", got)
	}
}

func TestDeveloperNotFoundIsExit30(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("GET", "/users", 404, `{"error":{"code":404,"message":"not found"}}`))
	rc := teamtest.NewRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Email: "alice@example.com"})
	if got := exitCodeOf(t, err); got != 30 {
		t.Errorf("404 exit = %d, want 30", got)
	}
}

func TestMissingEmailIsUsageError(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(usersBody(aliceUser)))
	rc := teamtest.NewRC(t, rt)
	_, err := viewcmd.Run(rc, viewcmd.Input{Email: "   "})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("empty email exit = %d, want 2", got)
	}
}
