package kernel_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// codedError is a stand-in for any application error that already knows its own
// exit code (exit.SafetyFlag, a client-side validation error, ...). Used to
// assert WrapArgErrors demotes nothing: exit 2 is the fallback for cobra's
// untyped rejections, not a blanket override.
type codedError struct {
	msg  string
	code int
}

func (e *codedError) Error() string { return e.msg }
func (e *codedError) ExitCode() int { return e.code }

// newLeaf builds a runnable leaf with the given validator, shaped like a real
// gplay command (silent so cobra prints nothing during the test).
func newLeaf(use string, args cobra.PositionalArgs) *cobra.Command {
	return &cobra.Command{
		Use:           use,
		Args:          args,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
}

// TestWrapArgErrors_argCountRejectionIsUsage is the core of #426: a validator
// rejection (missing argument or surplus argument) comes back as CLI misuse
// (exit 2 per docs/DESIGN.md §9) with cobra's message intact, instead of the
// untyped error exit.For could only map to the generic exit 1.
func TestWrapArgErrors_argCountRejectionIsUsage(t *testing.T) {
	cases := []struct {
		name    string
		args    cobra.PositionalArgs
		give    []string
		wantMsg string
	}{
		{"missing-argument", cobra.ExactArgs(1), nil, "accepts 1 arg(s), received 0"},
		{"missing-argument-minimum", cobra.MinimumNArgs(1), nil, "requires at least 1 arg(s), only received 0"},
		{"surplus-argument", cobra.ExactArgs(1), []string{"a", "b"}, "accepts 1 arg(s), received 2"},
		{"surplus-argument-maximum", cobra.MaximumNArgs(1), []string{"a", "b"}, "accepts at most 1 arg(s), received 2"},
		{"surplus-argument-none-accepted", cobra.NoArgs, []string{"a"}, `unknown command "a" for "view"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := kernel.WrapArgErrors(newLeaf("view", tc.args))

			err := cmd.Args(cmd, tc.give)
			if err == nil {
				t.Fatalf("Args(%v) = nil, want a rejection", tc.give)
			}
			if code := exit.For(err); code != 2 {
				t.Errorf("exit code = %d, want 2 (CLI misuse); err=%v", code, err)
			}
			var usage *exit.UsageError
			if !errors.As(err, &usage) {
				t.Errorf("error type = %T, want *exit.UsageError", err)
			}
			if got := err.Error(); got != tc.wantMsg {
				t.Errorf("message = %q, want %q verbatim (same treatment as a flag-parse error)", got, tc.wantMsg)
			}
		})
	}
}

// TestWrapArgErrors_acceptedArgsStayAccepted asserts the wrapper is invisible on
// the happy path: a valid argument count still validates, so the command runs.
func TestWrapArgErrors_acceptedArgsStayAccepted(t *testing.T) {
	cmd := kernel.WrapArgErrors(newLeaf("view", cobra.ExactArgs(1)))
	if err := cmd.Args(cmd, []string{"GPA.1234-5678-9012-34567"}); err != nil {
		t.Errorf("Args(1 arg) = %v, want nil", err)
	}
}

// TestWrapArgErrors_keepsTypedExitCodes asserts the wrapper only supplies a code
// where there is none. A validator that returns an error carrying its own
// exit.Coder: a missing safety flag (exit 3), a client-side validation failure
// (exit 20): keeps it, verbatim error and all. Exit 3 in particular "has no
// exceptions" (docs/DESIGN.md §9), so silently demoting it to 2 here would break
// the one distinction an automated caller most needs.
func TestWrapArgErrors_keepsTypedExitCodes(t *testing.T) {
	for _, code := range []int{3, 20} {
		want := &codedError{msg: "typed rejection", code: code}
		cmd := kernel.WrapArgErrors(newLeaf("view", func(*cobra.Command, []string) error { return want }))

		err := cmd.Args(cmd, nil)
		if got := exit.For(err); got != code {
			t.Errorf("exit code = %d, want %d (a typed error keeps its own code)", got, code)
		}
		if !errors.Is(err, want) {
			t.Errorf("error = %v, want the original typed error untouched", err)
		}
	}
}

// TestWrapArgErrors_isIdempotent asserts a second pass over an already-wrapped
// tree changes nothing observable: no doubled message, no re-typed code. The
// property holds by construction: the inner wrapper's *exit.UsageError already
// carries a Coder, so the outer asUsageError passes it through untouched. This
// test pins that contract for a tree assembled from shared constructors, where
// a subtree can legitimately be walked twice.
func TestWrapArgErrors_isIdempotent(t *testing.T) {
	cmd := kernel.WrapArgErrors(newLeaf("view", cobra.ExactArgs(1)))
	once := cmd.Args(cmd, nil)

	kernel.WrapArgErrors(cmd)
	twice := cmd.Args(cmd, nil)

	if once.Error() != twice.Error() {
		t.Errorf("message after a second pass = %q, want %q unchanged", twice, once)
	}
	if code := exit.For(twice); code != 2 {
		t.Errorf("exit code after a second pass = %d, want 2", code)
	}
}

// TestWrapArgErrors_walksTheWholeSubtree asserts the walk reaches every depth:
// the property that makes one call at the root cover `gplay games achievements
// view` as surely as `gplay version`.
func TestWrapArgErrors_walksTheWholeSubtree(t *testing.T) {
	leaf := newLeaf("view", cobra.ExactArgs(1))
	mid := &cobra.Command{Use: "achievements", RunE: kernel.GroupRunE}
	mid.AddCommand(leaf)
	top := &cobra.Command{Use: "games", RunE: kernel.GroupRunE}
	top.AddCommand(mid)
	root := &cobra.Command{Use: "gplay", Args: cobra.ArbitraryArgs, RunE: kernel.GroupRunE}
	root.AddCommand(top)

	kernel.WrapArgErrors(root)

	if code := exit.For(leaf.Args(leaf, nil)); code != 2 {
		t.Errorf("depth-3 leaf exit code = %d, want 2 (the walk did not reach it)", code)
	}
}

// TestWrapArgErrors_leavesGroupingNounsAlone asserts the wrapper does not touch
// a nil Args. A grouping noun keeps Args nil so cobra passes leftover tokens to
// GroupRunE, which owns both halves of that contract: bare → help (exit 0),
// unknown subcommand → misuse (exit 2). Installing a validator there would
// break the help half, since cobra validates args before it ever reaches RunE.
//
// The group is mounted under a root here because that is the only shape gplay
// ever ships: cobra applies legacyArgs to a nil-Args command from Find (BEFORE
// execute, so out of reach of any Args wrapper), and legacyArgs raises its own
// untyped "unknown command" only for a command with subcommands and NO parent.
// That is precisely the case the root neutralises with an explicit
// Args:ArbitraryArgs (cmd/gplay/main.go); every group below it is exempt.
func TestWrapArgErrors_leavesGroupingNounsAlone(t *testing.T) {
	group := &cobra.Command{
		Use:           "achievements",
		Short:         "Manage a game's achievement configurations",
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	group.AddCommand(newLeaf("view", cobra.ExactArgs(1)))
	root := &cobra.Command{
		Use:           "gplay",
		Args:          cobra.ArbitraryArgs,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(group)

	kernel.WrapArgErrors(root)

	if group.Args != nil {
		t.Fatal("grouping noun got an Args validator, want nil (GroupRunE owns its leftover tokens)")
	}

	// Bare invocation still prints help and succeeds.
	var help strings.Builder
	root.SetArgs([]string{"achievements"})
	root.SetOut(&help)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("bare group = %v, want nil (help printed)", err)
	}
	if help.Len() == 0 {
		t.Error("bare group printed no help")
	}

	// An unknown subcommand is still the exit-2 misuse GroupRunE raises.
	root.SetArgs([]string{"achievements", "nonesuch"})
	root.SetOut(io.Discard)
	err := root.Execute()
	if code := exit.For(err); code != 2 {
		t.Errorf("unknown subcommand exit code = %d, want 2; err=%v", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown subcommand error = %v, want it to name the unknown command", err)
	}
}

// TestWrapArgErrors_nilCommand asserts the nil guard, so a caller can compose it
// around a constructor that may return nil without a panic at boot.
func TestWrapArgErrors_nilCommand(t *testing.T) {
	if got := kernel.WrapArgErrors(nil); got != nil {
		t.Errorf("WrapArgErrors(nil) = %v, want nil", got)
	}
}
