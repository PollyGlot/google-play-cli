// Package list_test exercises `gplay team users list` at the command level: it
// asserts external behaviour (table + json shape, admin marking, pagination to
// completion, error-code mapping, and the unresolved-developer-id path)
// against a stubbed users.list response via the shared teamtest transport.
package list_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	listcmd "github.com/PollyGlot/google-play-cli/commands/team/users/list"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
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

const twoUsers = `{"users":[
  {"email":"alice@example.com","accessState":"ACTIVE","developerAccountPermissions":["CAN_MANAGE_PERMISSIONS_GLOBAL"]},
  {"email":"bob@example.com","accessState":"PENDING","developerAccountPermissions":["CAN_REPLY_TO_REVIEWS_GLOBAL"],"grants":[{"packageName":"com.example.app","appLevelPermissions":["CAN_REPLY_TO_REVIEWS"]}]}
]}`

// TestUsersList_table_marksAdmin asserts the table lists every member and
// marks the account-wide admin distinctly (US24).
func TestUsersList_table_marksAdmin(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(twoUsers))
	rc := teamtest.NewRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatalf("users list: %v", err)
	}
	out := renderTable(t, r)
	for _, want := range []string{"EMAIL", "ADMIN", "alice@example.com", "bob@example.com", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
	// bob is not an admin: his row must not carry the admin marker. Assert the
	// admin column count is exactly one "yes" across the table.
	if got := strings.Count(out, "yes"); got != 1 {
		t.Errorf("expected exactly one admin marker, got %d\n%s", got, out)
	}
}

// TestUsersList_json_verbatim asserts --output json passes the users.list
// response through (ADR-0003): every member present, raw enums intact.
func TestUsersList_json_verbatim(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(twoUsers))
	rc := teamtest.NewRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Users []struct {
			Email                       string   `json:"email"`
			DeveloperAccountPermissions []string `json:"developerAccountPermissions"`
		} `json:"users"`
	}
	if err := json.Unmarshal([]byte(renderJSON(t, r)), &view); err != nil {
		t.Fatalf("json not parseable: %v", err)
	}
	if len(view.Users) != 2 {
		t.Fatalf("got %d users, want 2", len(view.Users))
	}
	if view.Users[0].Email != "alice@example.com" || view.Users[0].DeveloperAccountPermissions[0] != "CAN_MANAGE_PERMISSIONS_GLOBAL" {
		t.Errorf("verbatim passthrough lost data: %+v", view.Users[0])
	}
}

// TestUsersList_paginatesToCompletion asserts ListUsers follows nextPageToken
// and the merged result carries every page's members (no silent truncation).
func TestUsersList_paginatesToCompletion(t *testing.T) {
	page1 := `{"users":[{"email":"a@example.com"}],"nextPageToken":"tok"}`
	page2 := `{"users":[{"email":"b@example.com"}]}`
	rt := teamtest.New(teamtest.Pages(page1, page2))
	rc := teamtest.NewRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatal(err)
	}
	out := renderJSON(t, r)
	for _, want := range []string{"a@example.com", "b@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("merged json missing %q (pagination truncated?)\n%s", want, out)
		}
	}
	// Two GET pages, each to /users.
	if got := len(rt.Calls()); got != 2 {
		t.Errorf("expected 2 users.list page requests, got %d", got)
	}
}

// TestUsersList_repeatedToken_abortsLoop asserts that a server repeating the
// same nextPageToken makes ListUsers abort with the standard pagination
// token-loop api.Error instead of following the token forever. UsersList serves
// the same body (carrying a nextPageToken) on every page, so the second
// sighting of the token trips the guard.
func TestUsersList_repeatedToken_abortsLoop(t *testing.T) {
	looping := `{"users":[{"email":"a@example.com"}],"nextPageToken":"stuck"}`
	rt := teamtest.New(teamtest.UsersList(looping))
	rc := teamtest.NewRC(t, rt)

	_, err := listcmd.Run(rc, listcmd.Input{})
	if err == nil {
		t.Fatal("expected a pagination token-loop error, got nil")
	}
	if !strings.Contains(err.Error(), "pagination token loop detected in users.list") {
		t.Errorf("error %q should report the repeated-nextPageToken loop", err.Error())
	}
	// The guard aborts on the second sighting: page 1 seeds the token, page 2
	// re-sees it and stops. No unbounded following.
	if got := len(rt.Calls()); got != 2 {
		t.Errorf("expected exactly 2 page requests before the loop guard aborts, got %d", got)
	}
}

// TestUsersList_empty_jsonArray asserts an account with no members renders
// {"users":[]} (not null): the conventional empty-array shape.
func TestUsersList_empty_jsonArray(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(`{"users":[]}`))
	rc := teamtest.NewRC(t, rt)

	r, err := listcmd.Run(rc, listcmd.Input{})
	if err != nil {
		t.Fatal(err)
	}
	out := renderJSON(t, r)
	if !strings.Contains(out, `"users":[]`) || strings.Contains(out, "null") {
		t.Errorf("empty account should render {\"users\":[]} (not null), got %s", out)
	}
}

// TestUsersList_unresolvedDeveloperID_exit10 asserts that with no developer-id
// anywhere the command fails with exit 10 before any network call.
func TestUsersList_unresolvedDeveloperID_exit10(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	rc.Resolved = &config.Resolved{} // no developer-id from any layer

	_, err := listcmd.Run(rc, listcmd.Input{})
	if got := exitCodeOf(t, err); got != 10 {
		t.Errorf("exit = %d, want 10 (unresolved developer-id); err=%v", got, err)
	}
	if rt.Wrote() || len(rt.Calls()) != 0 {
		t.Errorf("unresolved id must make no API call; calls=%v", rt.Calls())
	}
}

// TestUsersList_flagDeveloperID_overrides asserts an explicit --developer-id is
// used and lands in the request path.
func TestUsersList_flagDeveloperID_overrides(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rt := teamtest.New(teamtest.UsersList(`{"users":[]}`))
	rc := teamtest.NewRC(t, rt)
	rc.Resolved = &config.Resolved{} // force the flag to be the only source

	_, err := listcmd.Run(rc, listcmd.Input{DeveloperID: "12345"})
	if err != nil {
		t.Fatalf("users list: %v", err)
	}
	calls := rt.Calls()
	if len(calls) == 0 || !strings.Contains(calls[0].Path, "/developers/12345/users") {
		t.Errorf("request path should carry the --developer-id; calls=%v", calls)
	}
}

// TestUsersList_apiErrors maps upstream failures to exit codes: 403→11 (with
// the grant hint), 404→30 (with the verify-developer-id hint), 500→40.
func TestUsersList_apiErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		wantExit int
		wantHint string
	}{
		{"forbidden", 403, 11, "Users & permissions"},
		{"notFound", 404, 30, "developerId"},
		{"serverError", 500, 40, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := teamtest.New(teamtest.Fail("GET", "/users", tc.status, `{"error":{"message":"nope"}}`))
			rc := teamtest.NewRC(t, rt)
			_, err := listcmd.Run(rc, listcmd.Input{})
			if got := exitCodeOf(t, err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d; err=%v", got, tc.wantExit, err)
			}
			if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantHint)
			}
		})
	}
}

// TestUsersList_unknownColumn_exit2 asserts an unknown --columns key is a usage
// error before any network call.
func TestUsersList_unknownColumn_exit2(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	_, err := listcmd.Run(rc, listcmd.Input{Columns: "email,nope"})
	if got := exitCodeOf(t, err); got != 2 {
		t.Errorf("exit = %d, want 2; err=%v", got, err)
	}
}

// TestNewCommand_flags asserts the flag/name surface.
func TestNewCommand_flags(t *testing.T) {
	cmd := listcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"developer-id", "columns", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
	if cmd.Use != "list" {
		t.Errorf("cmd.Use = %q, want list", cmd.Use)
	}
}
