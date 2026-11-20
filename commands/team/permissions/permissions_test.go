package permissions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	permscmd "github.com/PollyGlot/google-play-cli/commands/team/permissions"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

func newOfflineRC(t *testing.T) *kernel.RunContext {
	t.Helper()
	rc := kernel.NewForTest(context.Background(), kernel.Boot{}, kernel.Inputs{Format: output.FormatJSON})
	rc.Resolved = &config.Resolved{}
	return rc
}

func renderJSON(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().JSON(&b); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	return b.String()
}

func renderTable(t *testing.T, r output.Renderable) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Renderers().Table(&b); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	return b.String()
}

func TestPermissions_offline_account(t *testing.T) {
	rc := newOfflineRC(t)
	r, err := permscmd.Run(rc, permscmd.Input{})
	if err != nil {
		t.Fatalf("permissions must run offline: %v", err)
	}

	table := renderTable(t, r)
	for _, want := range []string{
		"ALIAS", "ACCOUNT ENUM", "APP ENUM", "BUNDLES", "LABEL",
		"release-production", "CAN_MANAGE_PUBLIC_APKS_GLOBAL", "CAN_MANAGE_PUBLIC_APKS",
		"Role bundles", "release-manager", "admin",
		"scope: account",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("table output missing %q\n---\n%s", want, table)
		}
	}
	if !strings.Contains(table, "edit-games") {
		t.Errorf("table should list the account-only alias edit-games\n%s", table)
	}
}

func TestPermissions_json_marksAdmin(t *testing.T) {
	rc := newOfflineRC(t)
	r, err := permscmd.Run(rc, permscmd.Input{Scope: "account"})
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Scope   string `json:"scope"`
		Aliases []struct {
			Alias           string `json:"alias"`
			AccountEnum     string `json:"accountEnum"`
			AppEnum         string `json:"appEnum"`
			Enum            string `json:"enum"`
			AdminConferring bool   `json:"adminConferring"`
		} `json:"aliases"`
		Bundles []struct {
			Role            string   `json:"role"`
			Enums           []string `json:"enums"`
			AdminConferring bool     `json:"adminConferring"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal([]byte(renderJSON(t, r)), &view); err != nil {
		t.Fatalf("JSON not parseable: %v", err)
	}
	if view.Scope != "account" {
		t.Errorf("scope = %q, want account", view.Scope)
	}

	var adminAliases, adminBundles int
	for _, a := range view.Aliases {
		if a.Alias == "manage-permissions" {
			if !a.AdminConferring {
				t.Error("manage-permissions must be marked adminConferring")
			}
			if a.Enum != "CAN_MANAGE_PERMISSIONS_GLOBAL" {
				t.Errorf("manage-permissions enum = %q, want account _GLOBAL", a.Enum)
			}
		}
		if a.AdminConferring {
			adminAliases++
		}
	}
	for _, b := range view.Bundles {
		if b.Role == "admin" && !b.AdminConferring {
			t.Error("admin bundle must be marked adminConferring")
		}
		if b.AdminConferring {
			adminBundles++
		}
	}
	if adminAliases != 1 {
		t.Errorf("exactly one alias is admin-conferring, got %d", adminAliases)
	}
	if adminBundles != 1 {
		t.Errorf("exactly one bundle is admin-conferring, got %d", adminBundles)
	}
}

func TestPermissions_scopeApp_resolvesBareEnum(t *testing.T) {
	rc := newOfflineRC(t)
	r, err := permscmd.Run(rc, permscmd.Input{Scope: "app"})
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Scope   string `json:"scope"`
		Aliases []struct {
			Alias string `json:"alias"`
			Enum  string `json:"enum"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(renderJSON(t, r)), &view); err != nil {
		t.Fatal(err)
	}
	if view.Scope != "app" {
		t.Errorf("scope = %q, want app", view.Scope)
	}
	for _, a := range view.Aliases {
		if a.Alias == "release-production" && a.Enum != "CAN_MANAGE_PUBLIC_APKS" {
			t.Errorf("release-production app enum = %q, want bare CAN_MANAGE_PUBLIC_APKS", a.Enum)
		}
		if a.Alias == "edit-games" && a.Enum != "" {
			t.Errorf("account-only edit-games should have no app enum under --scope app, got %q", a.Enum)
		}
	}
}

func TestPermissions_unknownScope_exit2(t *testing.T) {
	rc := newOfflineRC(t)
	_, err := permscmd.Run(rc, permscmd.Input{Scope: "global"})
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
}

func TestNewCommand_flags(t *testing.T) {
	cmd := permscmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"scope", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
	if cmd.Use != "permissions" {
		t.Errorf("cmd.Use = %q, want permissions", cmd.Use)
	}
}

func TestPermissions_flagsDeprecatedAlias(t *testing.T) {
	rc := newOfflineRC(t)
	r, err := permscmd.Run(rc, permscmd.Input{})
	if err != nil {
		t.Fatal(err)
	}

	const note = "[deprecated: no longer supported by Google]"
	table := renderTable(t, r)
	var marked []string
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, note) {
			marked = append(marked, strings.Fields(line)[0])
		}
	}
	if strings.Join(marked, ",") != "manage-managed-play" {
		t.Errorf("table rows carrying the deprecation note = %v, want [manage-managed-play]\n%s", marked, table)
	}
	if strings.Contains(table, "prefer") {
		t.Errorf("table must not steer to a successor: none exists\n%s", table)
	}

	var view struct {
		Aliases []struct {
			Alias      string `json:"alias"`
			Label      string `json:"label"`
			Deprecated *bool  `json:"deprecated"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(renderJSON(t, r)), &view); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range view.Aliases {
		if a.Deprecated == nil {
			t.Fatalf("alias %q: JSON must carry the deprecated field on every alias", a.Alias)
		}
		if a.Alias == "manage-managed-play" {
			found = true
			if !*a.Deprecated {
				t.Error("manage-managed-play must be marked deprecated in JSON")
			}
			if a.Label != "Change managed Play settings (account-wide)" {
				t.Errorf("JSON label = %q: the label must stay verbatim, the marking lives in `deprecated`", a.Label)
			}
			continue
		}
		if *a.Deprecated {
			t.Errorf("alias %q must not be marked deprecated", a.Alias)
		}
	}
	if !found {
		t.Error("manage-managed-play must stay listed (Public contract)")
	}
}
