// repeated.go: the fourth door into the CLI-misuse contract (docs/DESIGN.md §9,
// exit 2), closed the same way args.go closes the positional-argument one.
//
// pflag's default for a single-value flag is LAST-WINS: `gplay releases promote
// --track alpha --track production` silently promotes to production, and nothing
// on stdout or stderr says the first value was dropped. For a CLI whose main
// caller is an AI agent assembling an argv from a prompt, that is a silent
// mis-ship, not a convenience (PRD #446).
//
// RejectRepeatedFlags makes the repetition a usage error instead. Like
// WrapArgErrors it is ONE walk over the assembled tree at registration time, so
// the contract holds for leaf commands nobody has written yet: there is no
// per-command check to forget.
package kernel

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// RejectRepeatedFlags makes every single-value flag in cmd's subtree reject a
// second occurrence, and returns cmd so it composes at the end of registration
// next to WrapArgErrors:
//
//	return kernel.WrapArgErrors(kernel.RejectRepeatedFlags(root))
//
// The rejection is a pflag Set failure, which cobra routes through the root's
// FlagErrorFunc (cmd/gplay/main.go): exit 2, one `gplay: ...` line, no HTTP and
// no keyring probe, because parsing happens before any RunE body runs.
//
// Genuine multi-value flags are untouched: see repeatable.
//
// It also installs the help/usage hook that keeps `--help` byte-identical: see
// renderUnwrapped.
//
// It is idempotent: an already-wrapped flag is skipped, so a second call (or a
// flag shared between a parent's PersistentFlags and a child's merged Flags)
// never stacks two counters.
//
// Call it AFTER the tree is assembled: it walks what is registered at the
// moment of the call. The per-flag state lives in the wrapper, and newRootCmd
// builds a fresh tree per process, so one invocation never sees another's count.
func RejectRepeatedFlags(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	renderUnwrapped(cmd)
	wrapSubtree(cmd)
	return cmd
}

// wrapSubtree is the recursive half of RejectRepeatedFlags: the walk, without
// the once-per-tree help hook.
func wrapSubtree(cmd *cobra.Command) {
	// LocalFlags/PersistentFlags cover what this command OWNS; inherited
	// persistent flags are wrapped once, on the ancestor that declares them.
	cmd.PersistentFlags().VisitAll(wrapOnce)
	cmd.Flags().VisitAll(wrapOnce)
	for _, sub := range cmd.Commands() {
		wrapSubtree(sub)
	}
}

// wrapOnce swaps f.Value for an onceValue, unless the flag is a genuine
// multi-value flag or has already been wrapped.
func wrapOnce(f *pflag.Flag) {
	if f == nil || f.Value == nil {
		return
	}
	if _, already := f.Value.(*onceValue); already {
		return
	}
	if repeatable(f.Value) {
		return
	}
	f.Value = &onceValue{Value: f.Value, name: f.Name}
}

// mapFlagTypes are the pflag values whose Set MERGES into a collection instead
// of overwriting a scalar, and which therefore must keep accepting repetition,
// but which pflag exposes through no interface at all.
//
// pflag gives its slice values a published interface (pflag.SliceValue) and its
// map values nothing: *stringToStringValue & co. carry only Set/String/Type, so
// the type name IS the only structural signal available. Keeping the exhaustive
// list here (pflag v1.0.9 ships exactly these three) rather than a list of gplay
// flag NAMES preserves the property that matters: a `--label k=v` added tomorrow
// works without editing this file. The registry test in cmd/gplay reports any
// flag type it has never seen, so a fourth map kind in a future pflag cannot
// slip past unnoticed.
var mapFlagTypes = map[string]bool{
	"stringToString": true,
	"stringToInt":    true,
	"stringToInt64":  true,
}

// repeatable reports whether v is a multi-value flag value, i.e. one whose
// second occurrence on the command line is normal use rather than misuse:
// `metadata images apply --locale a --locale b` must keep working, and so must
// any repeatable flag added later.
func repeatable(v pflag.Value) bool {
	if _, slice := v.(pflag.SliceValue); slice {
		return true
	}
	return mapFlagTypes[v.Type()]
}

// renderUnwrapped makes the wrapper invisible to `--help`.
//
// pflag decides whether to append ` (default X)` to a usage line with
// Flag.defaultIsZeroValue(), a type SWITCH over its own unexported value types
// (*durationValue, *stringValue, …). Any wrapper falls through to the generic
// branch, which only knows the tokens "", "0", "false" and "<nil>" as zero
// defaults. A `--timeout` declared as Duration("timeout", 0, …) therefore stops
// being recognised as a zero default and starts advertising `(default 0s)` on
// every command in the tree, contradicting its own help text (and the generated
// website reference, which is built from `--help`).
//
// The fix keeps the wrapper for PARSING and takes it off for RENDERING: help
// and usage run after parsing, so the counters have already done their work.
// The restore is not cosmetic, it is what keeps the swap invisible to a caller
// that renders help and then keeps going (a library or a test), rather than to
// the process, which exits right after.
//
// The hook is installed on the command RejectRepeatedFlags was handed, and
// cobra inherits helpFunc/usageFunc down the tree, so one install covers every
// leaf, including `gplay help <cmd>` and the usage block cobra prints after an
// error.
func renderUnwrapped(cmd *cobra.Command) {
	help, usage := cmd.HelpFunc(), cmd.UsageFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		defer unwrapSubtree(c.Root())()
		help(c, args)
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		defer unwrapSubtree(c.Root())()
		return usage(c)
	})
}

// unwrapSubtree puts the DECLARED pflag.Value back on every wrapped flag in
// cmd's subtree and returns the function that reinstates the wrappers, counters
// and all.
func unwrapSubtree(cmd *cobra.Command) func() {
	var undo []func()
	visit := func(f *pflag.Flag) {
		wrapper, wrapped := f.Value.(*onceValue)
		if !wrapped {
			return
		}
		f.Value = wrapper.Value
		undo = append(undo, func() { f.Value = wrapper })
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		// A flag reached twice (owned persistently, then again through the
		// merged set) is a no-op the second time: it is no longer wrapped.
		c.PersistentFlags().VisitAll(visit)
		c.Flags().VisitAll(visit)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(cmd)
	return func() {
		for _, restore := range undo {
			restore()
		}
	}
}

// onceValue is a pflag.Value that accepts exactly one Set and reports the
// second as CLI misuse, naming the flag and BOTH values so an agent can
// self-correct without a human (PRD #446, user story 3).
//
// It embeds the real Value so String(), Type(), and the pflag behaviours that
// read them (GetDuration & co., bool NoOptDefVal) keep working; only Set is
// intercepted. What it CANNOT preserve is pflag's rendering of defaults, which
// switches on the CONCRETE type (*durationValue, *stringValue, …) and so falls
// through to a generic branch for any wrapper: see renderUnwrapped.
//
// The message names only the FIRST value on purpose: pflag wraps a Set failure
// as `invalid argument "<second>" for "--flag" flag: <our message>`, so the
// second value and the flag name are already in the line, and repeating them
// would read as a stutter.
type onceValue struct {
	pflag.Value
	name  string
	first string
	set   bool
}

func (v *onceValue) Set(s string) error {
	if v.set {
		return fmt.Errorf("repeated flag: --%s was already set to %q; pass it once", v.name, v.first)
	}
	if err := v.Value.Set(s); err != nil {
		return err
	}
	v.set = true
	v.first = s
	return nil
}
