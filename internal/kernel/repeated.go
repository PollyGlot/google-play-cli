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
// Genuine multi-value flags are untouched. They are recognised structurally, by
// pflag.SliceValue (what StringSliceVar / StringArrayVar produce), not by a
// hand-kept name list: `metadata images apply --locale a --locale b` keeps
// working, and so does any repeatable flag added later, for free.
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
	// LocalFlags/PersistentFlags cover what this command OWNS; inherited
	// persistent flags are wrapped once, on the ancestor that declares them.
	cmd.PersistentFlags().VisitAll(wrapOnce)
	cmd.Flags().VisitAll(wrapOnce)
	for _, sub := range cmd.Commands() {
		RejectRepeatedFlags(sub)
	}
	return cmd
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
	if _, slice := f.Value.(pflag.SliceValue); slice {
		return
	}
	f.Value = &onceValue{Value: f.Value, name: f.Name}
}

// onceValue is a pflag.Value that accepts exactly one Set and reports the
// second as CLI misuse, naming the flag and BOTH values so an agent can
// self-correct without a human (PRD #446, user story 3).
//
// It embeds the real Value so String(), Type(), and every pflag behaviour that
// keys on the concrete type through an interface assertion (defaults rendering,
// bool NoOptDefVal) keep working; only Set is intercepted.
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
