// args.go: the positional-argument door into the CLI-misuse contract
// (docs/DESIGN.md §9, exit 2). gplay has three doors, and they used to disagree:
//
//  1. a flag-parse failure  → the root's SetFlagErrorFunc wraps it in
//     exit.Usagef (cmd/gplay/main.go), so exit 2;
//  2. an unknown subcommand → GroupRunE returns exit.Usagef, so exit 2;
//  3. a wrong NUMBER of positional arguments → cobra calls the command's Args
//     validator from execute() and returns its error UNTOUCHED. It never
//     reaches FlagErrorFunc, carries no exit.Coder, and exit.For could only
//     fall back to the generic exit 1 (#426).
//
// WrapArgErrors closes door 3 the same way the other two are closed: in one
// place, for the whole tree, at registration time: never with a per-command
// check, which is what makes the contract hold for commands nobody has written
// yet.
package kernel

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// WrapArgErrors routes every positional-argument rejection in cmd's subtree
// through the exit-2 usage path, and returns cmd so it composes at the end of
// registration:
//
//	return kernel.WrapArgErrors(root)
//
// It replaces each non-nil Args validator with one that runs the original and
// re-types its error as *exit.UsageError. The message names the fix rather than
// the count (#593): cobra's "accepts 1 arg(s), received 0" never says WHICH
// argument is missing, and its NoArgs rejection reads `unknown command "x"` on
// a leaf that has no subcommands at all. So:
//
//   - a missing argument names the first absent placeholder of cmd.Use
//     (`missing <artifact>`);
//   - a stray argument on a leaf that takes none says so (`unexpected
//     argument "x": gplay tracks list takes no positional arguments`);
//   - any other count keeps cobra's own text;
//
// and every form ends with the usage line (`; usage: gplay releases upload
// <artifact> [flags]`), so the one stderr line, or the JSON envelope's message,
// is enough to re-run correctly.
//
// Three things it does NOT do:
//
//   - It never touches a nil Args. A grouping noun leaves Args nil so cobra
//     accepts leftover tokens and hands them to GroupRunE, which prints help on
//     a bare invocation and rejects an unknown subcommand itself (door 2).
//     Substituting a validator there would break both halves of that contract.
//     Nothing is lost: cobra's ValidateArgs treats a nil Args as ArbitraryArgs,
//     and the one nil-Args path that CAN fail (legacyArgs on a root with
//     subcommands, raised from Find before execute()) is already neutralised
//     by the root's explicit Args:ArbitraryArgs (see cmd/gplay/main.go).
//   - It never re-codes an error that already carries an exit.Coder. A
//     validator that returns exit.SafetyFlag (exit 3) or a client-side
//     validation error (exit 20) keeps its code; exit 2 is the fallback for
//     cobra's untyped errors, not an override.
//   - It never CHANGES anything on a second pass. Re-wrapping stacks another
//     closure, but the inner wrapper's *exit.UsageError already carries a
//     Coder, so the outer asUsageError passes it through untouched: same
//     message, same code. Idempotent by construction, with no bookkeeping.
//
// Call it AFTER the tree is assembled, like Experimental: it walks what is
// registered at the moment of the call. newRootCmd materialises cobra's
// lazily-added `help` and `completion` commands before calling it so their
// validators are covered too; only the hidden `__complete` plumbing command,
// which cobra injects inside Execute with no pre-Execute hook, stays outside.
func WrapArgErrors(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Args != nil {
		inner := cmd.Args
		takesNone := isNoArgs(inner)
		cmd.Args = func(c *cobra.Command, args []string) error {
			return asUsageError(c, args, takesNone, inner(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		WrapArgErrors(sub)
	}
	return cmd
}

// asUsageError re-types an argument-validation failure as CLI misuse: nil stays
// nil, an error that already knows its exit code is returned untouched, and
// anything else (cobra's plain "accepts 1 arg(s), received 2") becomes an
// *exit.UsageError (ExitCode 2) whose message names the fix (see WrapArgErrors).
func asUsageError(c *cobra.Command, args []string, takesNone bool, err error) error {
	if err == nil {
		return nil
	}
	var coder exit.Coder
	if errors.As(err, &coder) {
		return err
	}
	what := err.Error()
	placeholders := requiredPlaceholders(c.Use)
	switch {
	case takesNone && len(args) > 0 && !c.HasSubCommands():
		what = fmt.Sprintf("unexpected argument %q: %s takes no positional arguments", args[0], c.CommandPath())
	case len(args) < len(placeholders):
		what = "missing " + placeholders[len(args)]
	}
	return exit.Usagef("%s; usage: %s", what, c.UseLine())
}

// isNoArgs reports whether v is cobra.NoArgs itself. Comparing code pointers is
// the one way to recognise a top-level func value in Go, and it lets the NoArgs
// rejection be reworded without matching cobra's message text.
func isNoArgs(v cobra.PositionalArgs) bool {
	return reflect.ValueOf(v).Pointer() == reflect.ValueOf(cobra.NoArgs).Pointer()
}

// requiredPlaceholders returns the required positional placeholders a Use line
// declares, in order: `upload <artifact>` gives [<artifact>], `add <package>...`
// gives [<package>]. The scan stops at the first flag (`set <email> --package
// <pkg>` names only <email>) or optional group (`[<orderId>...]`): what follows
// is not a positional the command requires.
func requiredPlaceholders(use string) []string {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return nil
	}
	var out []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || strings.HasPrefix(f, "[") {
			break
		}
		if strings.HasPrefix(f, "<") {
			out = append(out, strings.TrimSuffix(f, "..."))
		}
	}
	return out
}
