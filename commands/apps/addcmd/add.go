// Package addcmd implements `gplay apps add`: register an Android
// package under the active Account, with a cheap edits.insert+delete
// access probe (skippable via --no-verify) so typos and missing
// per-app permission grants are caught at registration time rather
// than weeks later in CI.
package addcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// validateTimeout caps the edits.insert+delete probe at a fixed
// budget. The whole point of validate-by-default is to fail fast at
// registration time; a stalled TCP connection or hung pool would
// otherwise let `gplay apps add` hang indefinitely in CI (until the
// job-level timeout fires, which can be minutes or hours later).
const validateTimeout = 30 * time.Second

// Input is the request-shaped struct cobra builds from args + flags.
type Input struct {
	Package  string
	NoVerify bool
}

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// authError signals "no account resolved"; ExitCode()=10 per
// docs/DESIGN.md §9 and the resolver precedence rules.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// validationError is a client-side package-name validation failure with
// ExitCode()=20 per docs/DESIGN.md §9 (client-side validation).
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

// Run is the kernel-shaped business function: validate package format,
// optionally probe the API, then persist via registry + config.Save.
// Order matters: format check first (no HTTP), API probe second, write
// last. A failure at any step leaves the global config untouched so the
// registry never carries a package the credential cannot reach.
//
// The Account the package is registered under is rc.AccountName — the
// Account that actually backs rc.Account, even when --account /
// GPLAY_ACCOUNT picked something other than the global active. The
// pre-fix code used rc.Resolved.ConfigAccount, which silently
// misattributed (Account, Package) pairs whenever a flag-layer or
// env-layer override beat the cascade.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if err := validatePackage(in.Package); err != nil {
		return nil, err
	}
	if rc.AccountName == "" {
		// rc.AccountName is "" in two cases:
		//   1. No credential resolved at all (no flag, no env, no
		//      active in config) → user must `gplay auth login` or
		//      pass --account/--service-account.
		//   2. Credential came from --service-account or
		//      GPLAY_SERVICE_ACCOUNT (inline JSON, ad-hoc identity).
		//      Such credentials have no local Account name to register
		//      the package under; that path is unsupported for `apps
		//      add` because the registry is per-Account.
		// An empty name means the resolver can only reach an inline or
		// no-source layer, so EnsureAccount here is keystore-free — it
		// never probes for a stored Account.
		rc.EnsureAccount()
		if rc.Account != nil {
			return nil, &usageError{msg: "apps add: cannot register under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		return nil, &authError{msg: "no Account resolved; run `gplay auth login`, set GPLAY_ACCOUNT, or pass --account"}
	}
	account := rc.AccountName

	// Load the global config BEFORE the probe so a missing-Account
	// registry inconsistency is caught client-side. Otherwise the
	// probe burns an HTTP round-trip against Google only to fail
	// post-flight on registry.Add with ErrUnknownAccount — and the
	// resulting "registry: unknown account" message looks like CLI
	// misuse when really it's a `gplay auth login` workflow gap (the
	// keystore has the credential but the global config doesn't list
	// the Account, e.g. local override pinning a name that was never
	// `auth login`-registered).
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, fsOrDefault(rc), rc.ConfigPath)
	if err != nil {
		return nil, err
	}
	if !accountInGlobal(g, account) {
		return nil, &authError{msg: fmt.Sprintf(
			"apps add: Account %q is not in the global config (keystore-only Accounts are unsupported); run `gplay auth login` to register it",
			account,
		)}
	}

	if !in.NoVerify {
		hc, err := rc.AuthedClient()
		if err != nil {
			return nil, err
		}
		probeCtx, cancel := context.WithTimeout(rc.Ctx, validateTimeout)
		defer cancel()
		if err := edits.Validate(probeCtx, hc, in.Package); err != nil {
			return nil, err
		}
	}

	if err := registry.Add(&g.Accounts, account, in.Package); err != nil {
		return nil, err
	}
	if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
		return nil, err
	}

	verb := "registered"
	if in.NoVerify {
		verb = "registered (unverified)"
	}
	_, _ = fmt.Fprintf(rc.Stderr, "✓ %s %q under Account %q\n", verb, in.Package, account)
	return nil, nil
}

// validatePackage applies the cheapest client-side checks before any
// API round-trip: non-empty and at least one dot. Google Play uses
// reverse-DNS package names, so a missing dot is a typo, not a real
// package.
func validatePackage(pkg string) error {
	if pkg == "" {
		return &usageError{msg: "apps add: <package> argument is required"}
	}
	if !strings.Contains(pkg, ".") {
		return &validationError{msg: fmt.Sprintf("apps add: %q is not a valid Android package name (must contain a dot, e.g. com.example.myapp)", pkg)}
	}
	return nil
}

func fsOrDefault(rc *kernel.RunContext) config.FS {
	if rc.FS != nil {
		return rc.FS
	}
	return config.OSFS{}
}

func accountInGlobal(g *config.Global, name string) bool {
	for _, a := range g.Accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

// NewCommand returns the cobra command for `gplay apps add <pkg>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "add <package>",
		Short: "Register an Android package under the active Account",
		Long: `Register an Android package name (e.g. com.example.myapp) under
the active Account in gplay's local registry. By default ` + "`apps add`" + `
validates access by opening and immediately discarding a Google Play
Edit on the package — a cheap probe that catches typos and missing
per-app permission grants at registration time rather than weeks later
in CI.

Pass --no-verify to skip the API round-trip (useful for offline or
preparatory registration).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Package: args[0], NoVerify: noVerify})
			})
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip the edits.insert+delete access probe (record the package unconditionally)")
	return cmd
}
