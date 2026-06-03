// Package removecmd implements `gplay apps remove`: drop an Android
// package from the active Account's local registry. Pure local-state
// op — never calls the Google Play API. See the issue #24 acceptance
// criteria for the exact contract.
package removecmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Input is the request-shaped struct cobra builds from args.
type Input struct {
	Package string
}

// usageError is CLI misuse (exit code 2 per docs/DESIGN.md §9). Used
// for the inline-credential branch — mirrors addcmd/listcmd so
// exit.For dispatches all three the same way.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// authError signals "no Account resolved" or "Account name not in
// global config" (exit code 10 per docs/DESIGN.md §9). Mirrors
// addcmd/listcmd's error shape for the same reason.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// validationError signals a client-side package-name validation
// failure (exit code 20 per docs/DESIGN.md §9). Symmetric with
// addcmd's validatePackage: if addcmd refuses to register a name
// without a dot, remove refuses to remove one too — a typo at the
// remove site can never match an entry the registry was allowed to
// carry, so the right answer is exit 20, not the misleading
// `"foo" not in registry` no-op.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

// Run drops in.Package from rc.AccountName's Packages slice and
// persists the result to rc.ConfigPath. Returns (nil, nil) on success
// — remove has no payload (mirrors auth logout's free-form stderr
// contract).
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	// Validate package format BEFORE touching any state so a typo
	// fails fast with a meaningful exit code instead of the
	// `"" not in registry` no-op the user can't distinguish from a
	// real "absent" case.
	if err := validatePackage(in.Package); err != nil {
		return nil, err
	}
	// Account resolution — mirrors addcmd's branching so all three
	// per-Account-registry commands (add/list/remove) reject the same
	// nonsensical inputs with the same exit codes.
	if rc.AccountName == "" {
		// Empty name ⟹ only an inline or no-source layer is reachable,
		// so this resolves the credential without probing the keyring.
		if err := rc.EnsureAccount(); err != nil {
			// A present-but-invalid inline credential (--service-account /
			// GPLAY_SERVICE_ACCOUNT) is a hard error (exit 10, the real
			// cause) instead of the misdirecting "run gplay auth login"
			// below — never mutate a fallback Account's registry (ADR-0020).
			return nil, err
		}
		if rc.Account != nil {
			return nil, &usageError{msg: "apps remove: cannot remove under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		return nil, &authError{msg: "no Account resolved; run `gplay auth login`, set GPLAY_ACCOUNT, or pass --account"}
	}

	g, err := config.LoadGlobalOrEmpty(rc.Ctx, fsOrDefault(rc), rc.ConfigPath)
	if err != nil {
		return nil, err
	}
	if !accountInGlobal(g, rc.AccountName) {
		return nil, &authError{msg: fmt.Sprintf(
			"apps remove: Account %q is not in the global config (keystore-only Accounts are unsupported); run `gplay auth login` to register it",
			rc.AccountName,
		)}
	}
	// Branch on the pre-state so the absent-package case stays a true
	// no-op: skip the Save round-trip (no churn on disk) AND emit a
	// stderr hint so CI scripts piping `apps remove` don't lose the
	// "your input matched nothing" signal in a silent zero exit.
	if !registry.Has(g.Accounts, rc.AccountName, in.Package) {
		_, _ = fmt.Fprintf(rc.Stderr, "%q not in registry, nothing to do\n", in.Package)
		return nil, nil
	}
	// registry.Remove is a no-op on (unknown account, unknown pkg) and
	// has no other failure mode — the err return is documented but
	// always nil (see registry.go). The accountInGlobal pre-check
	// above already covered the unknown-account case, and we only
	// reach this branch when registry.Has confirmed the package is
	// present, so the call signature is "guaranteed mutation". Discard
	// the nil error instead of decorating an inreachable branch.
	_ = registry.Remove(&g.Accounts, rc.AccountName, in.Package)
	if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(rc.Stderr, "✓ removed %q from Account %q\n", in.Package, rc.AccountName)
	// Pin warning AFTER the success line so a stderr tail still shows
	// the dangling-pin signal as the most recent message. The warning
	// surfaces the inconsistency only — `apps remove` deliberately
	// does NOT rewrite the project's `.gplay/config.json` (see issue
	// #24: repinning is the caller's decision, not a side effect).
	if rc.Resolved != nil && rc.Resolved.Pin == in.Package && rc.Resolved.ProjectSharedPath != "" {
		_, _ = fmt.Fprintf(rc.Stderr,
			"warning: .gplay/config.json still pins %q; run `gplay init --package <other>` to repin or edit the file manually\n",
			in.Package,
		)
	}
	return nil, nil
}

// validatePackage applies the cheapest client-side checks before any
// state read: non-empty and at least one dot. Mirrors addcmd's
// validatePackage so the (add, remove) pair shares one validation
// surface — a future tightening (e.g. case rules, length cap) only
// needs to be threaded through both call sites.
func validatePackage(pkg string) error {
	if pkg == "" {
		return &usageError{msg: "apps remove: <package> argument is required"}
	}
	if !strings.Contains(pkg, ".") {
		return &validationError{msg: fmt.Sprintf("apps remove: %q is not a valid Android package name (must contain a dot, e.g. com.example.myapp)", pkg)}
	}
	return nil
}

func fsOrDefault(rc *kernel.RunContext) config.FS {
	if rc.FS != nil {
		return rc.FS
	}
	return config.OSFS{}
}

// accountInGlobal reports whether name is in the global config's
// Accounts slice. Private to removecmd — addcmd/listcmd each carry
// their own copy because cascade snapshots differ across the cases
// (Global vs Resolved).
func accountInGlobal(g *config.Global, name string) bool {
	for _, a := range g.Accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

// NewCommand returns the cobra command for `gplay apps remove <pkg>`.
// ExactArgs(1) is load-bearing: an empty positional would let Run
// reach the registry lookup with in.Package == "" and emit a confusing
// `"" not in registry` no-op on stderr.
func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <package>",
		Short: "Remove an Android package from the active Account's registry",
		Long: `Remove an Android package name (e.g. com.example.myapp) from the
active Account's local registry. This is a pure local-state operation:
the Google Play Console is never touched, no API call is made.

Idempotent: removing a package that isn't in the registry exits 0 with
a stderr note. If the removed package is currently pinned by the
repo's .gplay/config.json, a stderr warning is printed but the project
config is left untouched (repinning is the caller's decision).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Package: args[0]})
			})
		},
	}
	return cmd
}
