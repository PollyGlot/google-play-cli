// Package list_test exercises `gplay team grants list` at the command level:
// the table + json projection of grants out of the user list, and the
// --package / --user filters.
package list_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	listcmd "github.com/PollyGlot/google-play-cli/commands/team/grants/list"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/teamtest"
)

// twoUsersWithGrants: alice has a grant on foo, bob on foo and bar.
const twoUsersWithGrants = `{"users":[
  {"email":"alice@example.com","grants":[{"packageName":"com.example.foo","appLevelPermissions":["CAN_REPLY_TO_REVIEWS"]}]},
  {"email":"bob@example.com","grants":[
    {"packageName":"com.example.foo","appLevelPermissions":["CAN_VIEW_APP_QUALITY"]},
    {"packageName":"com.example.bar","appLevelPermissions":["CAN_MANAGE_PUBLIC_APKS"]}
  ]}
]}`

func tableOf(t *testing.T, rc *kernel.RunContext, in listcmd.Input) string {
	t.Helper()
	r, err := listcmd.Run(rc, in)
	if err != nil {
		t.Fatalf("grants list: %v", err)
	}
	var b bytes.Buffer
	if err := r.Renderers().Table(&b); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	return b.String()
}

func grantsOf(t *testing.T, rc *kernel.RunContext, in listcmd.Input) []map[string]any {
	t.Helper()
	r, err := listcmd.Run(rc, in)
	if err != nil {
		t.Fatalf("grants list: %v", err)
	}
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var view struct {
		Grants []map[string]any `json:"grants"`
	}
	if err := json.Unmarshal(b.Bytes(), &view); err != nil {
		t.Fatalf("json not parseable: %v\n%s", err, b.String())
	}
	return view.Grants
}

// TestGrantsList_all lists every grant across members.
func TestGrantsList_all(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(twoUsersWithGrants))
	rc := teamtest.NewRC(t, rt)

	out := tableOf(t, rc, listcmd.Input{})
	for _, want := range []string{"USER", "PACKAGE", "PERMISSIONS", "alice@example.com", "bob@example.com", "com.example.foo", "com.example.bar"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q\n%s", want, out)
		}
	}
	if g := grantsOf(t, rc, listcmd.Input{}); len(g) != 3 {
		t.Errorf("got %d grants, want 3", len(g))
	}
}

// TestGrantsList_packageFilter answers "who can touch this app".
func TestGrantsList_packageFilter(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(twoUsersWithGrants))
	rc := teamtest.NewRC(t, rt)

	g := grantsOf(t, rc, listcmd.Input{Package: "com.example.foo"})
	if len(g) != 2 {
		t.Fatalf("got %d grants for foo, want 2 (alice + bob)", len(g))
	}
	for _, row := range g {
		if row["packageName"] != "com.example.foo" {
			t.Errorf("--package filter leaked %v", row)
		}
	}
}

// TestGrantsList_userFilter answers "what apps this person touches".
func TestGrantsList_userFilter(t *testing.T) {
	rt := teamtest.New(teamtest.UsersList(twoUsersWithGrants))
	rc := teamtest.NewRC(t, rt)

	g := grantsOf(t, rc, listcmd.Input{User: "bob@example.com"})
	if len(g) != 2 {
		t.Fatalf("got %d grants for bob, want 2 (foo + bar)", len(g))
	}
	for _, row := range g {
		if row["email"] != "bob@example.com" {
			t.Errorf("--user filter leaked %v", row)
		}
	}
}

// TestGrantsList_unresolvedDeveloperID_exit10.
func TestGrantsList_unresolvedDeveloperID_exit10(t *testing.T) {
	t.Setenv("GPLAY_DEVELOPER_ID", "")
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	rc.Resolved.DeveloperID = ""

	_, err := listcmd.Run(rc, listcmd.Input{})
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 10 {
		t.Fatalf("err = %v, want exit 10", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("unresolved id must make no API call; calls=%v", rt.Calls())
	}
}

// TestNewCommand_flags asserts the flag/name surface.
func TestNewCommand_flags(t *testing.T) {
	cmd := listcmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "user", "columns", "developer-id", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}
