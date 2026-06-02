package vocab_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/team/vocab"
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

// TestAliasResolution_bothScopes checks the alias↔enum contract in both
// scopes: account appends _GLOBAL, app strips it (ADR-0016 §1).
func TestAliasResolution_bothScopes(t *testing.T) {
	for _, tc := range []struct {
		alias       string
		wantAccount string
		wantApp     string // "" => account-only, no app enum
	}{
		{"view-app-info", "CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", "CAN_VIEW_NON_FINANCIAL_DATA"},
		{"reply-reviews", "CAN_REPLY_TO_REVIEWS_GLOBAL", "CAN_REPLY_TO_REVIEWS"},
		{"release-production", "CAN_MANAGE_PUBLIC_APKS_GLOBAL", "CAN_MANAGE_PUBLIC_APKS"},
		{"manage-permissions", "CAN_MANAGE_PERMISSIONS_GLOBAL", "CAN_MANAGE_PERMISSIONS"},
		{"edit-games", "CAN_EDIT_GAMES_GLOBAL", ""}, // account-only
	} {
		t.Run(tc.alias, func(t *testing.T) {
			acc, _, err := vocab.ResolvePermissions(vocab.Account, []string{tc.alias})
			if err != nil {
				t.Fatalf("account resolve: %v", err)
			}
			if len(acc) != 1 || acc[0] != tc.wantAccount {
				t.Errorf("account = %v, want [%s]", acc, tc.wantAccount)
			}

			app, _, err := vocab.ResolvePermissions(vocab.App, []string{tc.alias})
			if tc.wantApp == "" {
				// Account-only alias under app scope is a usage error (exit 2).
				if got := exitCodeOf(t, err); got != 2 {
					t.Errorf("app resolve of account-only alias: exit = %d, want 2", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("app resolve: %v", err)
			}
			if len(app) != 1 || app[0] != tc.wantApp {
				t.Errorf("app = %v, want [%s]", app, tc.wantApp)
			}
		})
	}
}

// TestRawEnumPassthrough_verbatim asserts an unknown-to-gplay CAN_* enum is
// accepted verbatim (the escape hatch) and NOT scope-adjusted.
func TestRawEnumPassthrough_verbatim(t *testing.T) {
	raw := "CAN_DO_SOMETHING_BRAND_NEW_GLOBAL"
	got, warns, err := vocab.ResolvePermissions(vocab.App, []string{raw})
	if err != nil {
		t.Fatalf("raw enum must pass through: %v", err)
	}
	if len(got) != 1 || got[0] != raw {
		t.Errorf("raw passthrough = %v, want [%s] verbatim (no scope adjustment)", got, raw)
	}
	if len(warns) != 0 {
		t.Errorf("a non-deprecated raw enum must not warn: %v", warns)
	}
}

// TestUnspecifiedSentinel_rejected asserts a *_UNSPECIFIED sentinel is refused
// with exit 2 in both scopes.
func TestUnspecifiedSentinel_rejected(t *testing.T) {
	for _, tok := range []string{"DEVELOPER_LEVEL_PERMISSION_UNSPECIFIED", "APP_LEVEL_PERMISSION_UNSPECIFIED"} {
		_, _, err := vocab.ResolvePermissions(vocab.Account, []string{tok})
		if got := exitCodeOf(t, err); got != 2 {
			t.Errorf("%s: exit = %d, want 2", tok, got)
		}
	}
}

// TestDeprecated_acceptedWithWarning asserts the two deprecated values pass
// through (gplay never gates) but emit a steering warning to the modern enum.
func TestDeprecated_acceptedWithWarning(t *testing.T) {
	for _, tc := range []struct{ tok, modern string }{
		{"CAN_SEE_ALL_APPS", "CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL"},
		{"CAN_ACCESS_APP", "CAN_VIEW_NON_FINANCIAL_DATA"},
	} {
		got, warns, err := vocab.ResolvePermissions(vocab.Account, []string{tc.tok})
		if err != nil {
			t.Fatalf("%s must pass through: %v", tc.tok, err)
		}
		if len(got) != 1 || got[0] != tc.tok {
			t.Errorf("%s passthrough = %v, want verbatim", tc.tok, got)
		}
		if len(warns) != 1 || !strings.Contains(warns[0], tc.modern) {
			t.Errorf("%s warnings = %v, want one steering to %s", tc.tok, warns, tc.modern)
		}
	}
}

// TestUnknownAlias_exit2_pointsAtPermissions asserts a typo'd alias is a usage
// error whose message points at `gplay team permissions`.
func TestUnknownAlias_exit2_pointsAtPermissions(t *testing.T) {
	_, _, err := vocab.ResolvePermissions(vocab.Account, []string{"reply-to-everything"})
	if got := exitCodeOf(t, err); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "team permissions") {
		t.Errorf("error %q should point at `gplay team permissions`", err.Error())
	}
}

// TestResolvePermissions_dedupPreservesOrder asserts duplicates collapse while
// the first-seen order is preserved.
func TestResolvePermissions_dedupPreservesOrder(t *testing.T) {
	got, _, err := vocab.ResolvePermissions(vocab.App, []string{"reply-reviews", "view-app-info", "reply-reviews"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CAN_REPLY_TO_REVIEWS", "CAN_VIEW_NON_FINANCIAL_DATA"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestBundleExpansion asserts the frozen bundles expand over the read base and
// that admin is exactly the all-permissions enum.
func TestBundleExpansion(t *testing.T) {
	for _, tc := range []struct {
		role string
		want []string // account-scope enums
	}{
		{"viewer", []string{"CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", "CAN_VIEW_APP_QUALITY_GLOBAL"}},
		{"reviewer", []string{"CAN_VIEW_NON_FINANCIAL_DATA_GLOBAL", "CAN_VIEW_APP_QUALITY_GLOBAL", "CAN_REPLY_TO_REVIEWS_GLOBAL"}},
		{"admin", []string{"CAN_MANAGE_PERMISSIONS_GLOBAL"}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			got, err := vocab.ResolveRole(vocab.Account, tc.role)
			if err != nil {
				t.Fatalf("ResolveRole: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRole_appScope asserts a bundle resolves to bare app enums under app scope.
func TestRole_appScope(t *testing.T) {
	got, err := vocab.ResolveRole(vocab.App, "release-manager")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CAN_VIEW_NON_FINANCIAL_DATA", "CAN_VIEW_APP_QUALITY", "CAN_MANAGE_TRACK_APKS", "CAN_MANAGE_PUBLIC_APKS", "CAN_MANAGE_PUBLIC_LISTING"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestUnknownRole_exit2 asserts an unknown --role is a usage error listing the
// valid roles.
func TestUnknownRole_exit2(t *testing.T) {
	_, err := vocab.ResolveRole(vocab.Account, "superuser")
	if got := exitCodeOf(t, err); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	for _, role := range []string{"viewer", "admin"} {
		if !strings.Contains(err.Error(), role) {
			t.Errorf("error %q should list the valid role %q", err.Error(), role)
		}
	}
}

// TestBundles_excludeMoneyAndAccountOnly asserts no bundle silently confers a
// money or account-only capability (ADR-0016 §4).
func TestBundles_excludeMoneyAndAccountOnly(t *testing.T) {
	forbidden := map[string]bool{
		"view-financial": true, "manage-orders": true,
		"edit-games": true, "publish-games": true,
		"create-managed-play-apps": true, "manage-managed-play": true,
		"view-connected-apps": true, "edit-connected-apps": true,
	}
	for _, b := range vocab.Bundles() {
		for _, m := range b.Members {
			if forbidden[m] {
				t.Errorf("bundle %q must not include the sensitive/account-only capability %q", b.Name, m)
			}
		}
	}
}

// TestAdminDetection covers the --grant-admin trigger across every way admin
// can be expressed: the alias, the role, and a raw enum in either scope.
func TestAdminDetection(t *testing.T) {
	mustResolve := func(scope vocab.Scope, toks ...string) []string {
		t.Helper()
		got, _, err := vocab.ResolvePermissions(scope, toks)
		if err != nil {
			t.Fatalf("resolve %v: %v", toks, err)
		}
		return got
	}
	role := func(scope vocab.Scope, r string) []string {
		t.Helper()
		got, err := vocab.ResolveRole(scope, r)
		if err != nil {
			t.Fatalf("role %s: %v", r, err)
		}
		return got
	}

	confers := []struct {
		name  string
		enums []string
	}{
		{"alias account", mustResolve(vocab.Account, "manage-permissions")},
		{"alias app", mustResolve(vocab.App, "manage-permissions")},
		{"role admin account", role(vocab.Account, "admin")},
		{"raw account enum under app scope", mustResolve(vocab.App, "CAN_MANAGE_PERMISSIONS_GLOBAL")},
	}
	for _, c := range confers {
		if !vocab.IsAdminConferring(c.enums) {
			t.Errorf("%s: %v should be admin-conferring", c.name, c.enums)
		}
	}

	notAdmin := mustResolve(vocab.Account, "reply-reviews", "release-production")
	if vocab.IsAdminConferring(notAdmin) {
		t.Errorf("%v should NOT be admin-conferring", notAdmin)
	}
}

// TestRegistryCounts pins the alias registry against the ADR-0016 counts: 19
// curated aliases (the non-deprecated grantable permissions), 6 of them
// account-only, and 13 valid under app scope.
func TestRegistryCounts(t *testing.T) {
	all := vocab.Aliases()
	if len(all) != 19 {
		t.Errorf("len(Aliases) = %d, want 19 (ADR-0016: 19 non-deprecated grantable account-level)", len(all))
	}
	accountOnly, appValid := 0, 0
	for _, a := range all {
		if a.AccountOnly() {
			accountOnly++
		}
		if _, ok := a.AppEnum(); ok {
			appValid++
		}
	}
	if accountOnly != 6 {
		t.Errorf("account-only aliases = %d, want 6 (Play Games, managed Play, connected apps)", accountOnly)
	}
	if appValid != 13 {
		t.Errorf("app-valid aliases = %d, want 13 (ADR-0016: 13 non-deprecated grantable app-level)", appValid)
	}
}

// TestParseScope covers the --scope flag mapping including the default and the
// usage error.
func TestParseScope(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want vocab.Scope
	}{
		{"", vocab.Account}, {"account", vocab.Account}, {"app", vocab.App},
	} {
		got, err := vocab.ParseScope(tc.in)
		if err != nil {
			t.Fatalf("ParseScope(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseScope(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if _, err := vocab.ParseScope("global"); exitCodeOf(t, err) != 2 {
		t.Errorf("ParseScope(global) should be a usage error (exit 2)")
	}
}

// TestBundlesContaining asserts the including-bundles projection is correct and
// in bundle display order.
func TestBundlesContaining(t *testing.T) {
	got := vocab.BundlesContaining("view-app-info")
	want := []string{"viewer", "reviewer", "tester-manager", "release-manager"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("BundlesContaining(view-app-info) = %v, want %v", got, want)
	}
	if b := vocab.BundlesContaining("view-financial"); len(b) != 0 {
		t.Errorf("view-financial is in no bundle, got %v", b)
	}
}
