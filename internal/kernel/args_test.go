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

type codedError struct {
	msg  string
	code int
}

func (e *codedError) Error() string { return e.msg }
func (e *codedError) ExitCode() int { return e.code }

func newLeaf(use string, args cobra.PositionalArgs) *cobra.Command {
	return &cobra.Command{
		Use:           use,
		Args:          args,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
}

func TestWrapArgErrors_argCountRejectionIsUsage(t *testing.T) {
	cases := []struct {
		name    string
		use     string
		args    cobra.PositionalArgs
		give    []string
		wantMsg string
	}{
		{"missing-argument", "view <reviewId>", cobra.ExactArgs(1), nil, "missing <reviewId>; usage: view <reviewId>"},
		{"missing-argument-minimum", "view <orderId> [<orderId>...]", cobra.MinimumNArgs(1), nil, "missing <orderId>; usage: view <orderId> [<orderId>...]"},
		{"missing-variadic", "add <package>...", cobra.MinimumNArgs(1), nil, "missing <package>; usage: add <package>..."},
		{"missing-before-flag-in-use", "set <email> --package <pkg>", cobra.ExactArgs(1), nil, "missing <email>; usage: set <email> --package <pkg>"},
		{"missing-second", "copy <from> <to>", cobra.ExactArgs(2), []string{"a"}, "missing <to>; usage: copy <from> <to>"},
		{"missing-no-placeholder", "view", cobra.ExactArgs(1), nil, "accepts 1 arg(s), received 0; usage: view"},
		{"surplus-argument", "view <reviewId>", cobra.ExactArgs(1), []string{"a", "b"}, "accepts 1 arg(s), received 2; usage: view <reviewId>"},
		{"surplus-argument-maximum", "schema [query]", cobra.MaximumNArgs(1), []string{"a", "b"}, "accepts at most 1 arg(s), received 2; usage: schema [query]"},
		{"surplus-argument-none-accepted", "list", cobra.NoArgs, []string{"a"}, `unexpected argument "a": list takes no positional arguments; usage: list`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := kernel.WrapArgErrors(newLeaf(tc.use, tc.args))

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
				t.Errorf("message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

func TestWrapArgErrors_acceptedArgsStayAccepted(t *testing.T) {
	cmd := kernel.WrapArgErrors(newLeaf("view", cobra.ExactArgs(1)))
	if err := cmd.Args(cmd, []string{"GPA.1234-5678-9012-34567"}); err != nil {
		t.Errorf("Args(1 arg) = %v, want nil", err)
	}
}

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

func TestWrapArgErrors_nilCommand(t *testing.T) {
	if got := kernel.WrapArgErrors(nil); got != nil {
		t.Errorf("WrapArgErrors(nil) = %v, want nil", got)
	}
}
