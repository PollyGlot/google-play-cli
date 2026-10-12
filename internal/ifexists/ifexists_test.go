package ifexists_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/ifexists"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// parse registers --if-exists on a throwaway command with the given allowed
// set and parses args, the way cobra does for a real leaf.
func parse(t *testing.T, allowed []ifexists.Policy, args ...string) (ifexists.Policy, *cobra.Command, error) {
	t.Helper()
	var p ifexists.Policy
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	ifexists.Register(cmd, &p, "track", allowed...)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return p, cmd, err
}

var skipFail = []ifexists.Policy{ifexists.Skip, ifexists.Fail}

func TestRegister_defaultIsFail(t *testing.T) {
	p, cmd, err := parse(t, skipFail)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p != ifexists.Fail {
		t.Errorf("unset policy = %q, want fail", p)
	}
	if got := cmd.Flags().Lookup(ifexists.Name).DefValue; got != "fail" {
		t.Errorf("DefValue = %q, want fail", got)
	}
}

func TestRegister_parsesAllowedValue(t *testing.T) {
	p, _, err := parse(t, skipFail, "--if-exists", "skip")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p != ifexists.Skip {
		t.Errorf("policy = %q, want skip", p)
	}
}

// A value outside the command's allowed set fails at parse time, which the
// root's FlagErrorFunc turns into exit 2 before auth or any HTTP. update is a
// known policy, but not one a skip|fail command offers.
func TestRegister_rejectsValueOutsideAllowedSet(t *testing.T) {
	for _, v := range []string{"update", "bogus", "SKIP"} {
		_, _, err := parse(t, skipFail, "--if-exists", v)
		if err == nil || !strings.Contains(err.Error(), "skip or fail") {
			t.Errorf("--if-exists %s: err = %v, want a parse error naming skip or fail", v, err)
		}
	}
}

func TestRegister_updateAllowedWhereOffered(t *testing.T) {
	p, _, err := parse(t, []ifexists.Policy{ifexists.Skip, ifexists.Update, ifexists.Fail}, "--if-exists", "update")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p != ifexists.Update {
		t.Errorf("policy = %q, want update", p)
	}
}

// The flag type names the command's own allowed set, so help renders
// `--if-exists skip|fail` on one command and skip|update|fail on another, and
// the help text only describes the policies the command offers.
func TestRegister_typeAndUsageFollowAllowedSet(t *testing.T) {
	_, cmd, _ := parse(t, skipFail)
	f := cmd.Flags().Lookup(ifexists.Name)
	if got := f.Value.Type(); got != "skip|fail" {
		t.Errorf("Type() = %q, want skip|fail", got)
	}
	if strings.Contains(f.Usage, "update") {
		t.Errorf("usage mentions update on a skip|fail command: %q", f.Usage)
	}
	if !strings.Contains(f.Usage, "track") {
		t.Errorf("usage does not name the resource: %q", f.Usage)
	}
}

// The flag is the experimental part of an otherwise frozen leaf.
func TestRegister_flagIsExperimental(t *testing.T) {
	_, cmd, _ := parse(t, skipFail)
	if !kernel.IsExperimentalFlag(cmd, cmd.Flags().Lookup(ifexists.Name)) {
		t.Error("--if-exists is not classified Experimental")
	}
}

// Check guards a direct caller (Run invoked without cobra): the zero value is
// fail, and a policy outside the allowed set is CLI misuse (exit 2).
func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		in   ifexists.Policy
		want ifexists.Policy
		code int
	}{
		{"", ifexists.Fail, 0},
		{ifexists.Skip, ifexists.Skip, 0},
		{ifexists.Fail, ifexists.Fail, 0},
		{ifexists.Update, "", 2},
		{"bogus", "", 2},
	} {
		got, err := ifexists.Check(tc.in, skipFail...)
		if code := exit.For(err); code != tc.code {
			t.Errorf("Check(%q): exit %d, want %d (err=%v)", tc.in, code, tc.code, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("Check(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if err != nil && !strings.Contains(err.Error(), "skip or fail") {
			t.Errorf("Check(%q) error %q does not name skip or fail", tc.in, err)
		}
	}
}
