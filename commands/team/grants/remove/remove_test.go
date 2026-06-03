// Package remove_test exercises `gplay team grants remove` at the command
// level: the destructive --confirm gate (exit 3), CI=true not auto-confirming,
// the --dry-run preview, the delete-by-email+package path, and error mapping.
package remove_test

import (
	"errors"
	"strings"
	"testing"

	removecmd "github.com/PollyGlot/google-play-cli/commands/team/grants/remove"
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

// TestRemove_confirm_deletes asserts a confirmed remove issues grants.delete by
// email+package path.
func TestRemove_confirm_deletes(t *testing.T) {
	rt := teamtest.New(teamtest.Fail("DELETE", "/grants/com.example.foo", 204, ""))
	rc := teamtest.NewRC(t, rt)

	_, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Package: "com.example.foo", Confirm: true})
	if err != nil {
		t.Fatalf("remove --confirm: %v", err)
	}
	calls := rt.Calls()
	if len(calls) != 1 || calls[0].Method != "DELETE" || !strings.HasSuffix(calls[0].Path, "/users/bob@example.com/grants/com.example.foo") {
		t.Fatalf("expected one DELETE .../users/bob@example.com/grants/com.example.foo; calls=%v", calls)
	}
}

// TestRemove_withoutConfirm_exit3 asserts the destructive gate refuses without
// --confirm; CI=true must not auto-confirm.
func TestRemove_withoutConfirm_exit3(t *testing.T) {
	t.Setenv("CI", "true")
	rt := teamtest.New(teamtest.Fail("DELETE", "/grants/com.example.foo", 204, ""))
	rc := teamtest.NewRC(t, rt)

	_, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Package: "com.example.foo"})
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

// TestRemove_dryRun_previews asserts --dry-run previews with no HTTP.
func TestRemove_dryRun_previews(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)

	_, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Package: "com.example.foo", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("dry-run must make no HTTP call; calls=%v", rt.Calls())
	}
}

// TestRemove_missingPackage_exit2.
func TestRemove_missingPackage_exit2(t *testing.T) {
	rt := teamtest.New()
	rc := teamtest.NewRC(t, rt)
	if _, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Confirm: true}); exitCodeOf(t, err) != 2 {
		t.Errorf("missing --package should be exit 2")
	}
}

// TestRemove_apiErrors maps a 404 (no such grant) to exit 30 and a 403 to 11.
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
			rt := teamtest.New(teamtest.Fail("DELETE", "/grants/com.example.foo", tc.status, `{"error":{"message":"nope"}}`))
			rc := teamtest.NewRC(t, rt)
			_, err := removecmd.Run(rc, removecmd.Input{Email: "bob@example.com", Package: "com.example.foo", Confirm: true})
			if got := exitCodeOf(t, err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d; err=%v", got, tc.wantExit, err)
			}
		})
	}
}

// TestNewCommand_flags asserts the flag/name surface.
func TestNewCommand_flags(t *testing.T) {
	cmd := removecmd.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "confirm", "dry-run", "developer-id", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}
}
