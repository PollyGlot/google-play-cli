// Package addcmd implements `gplay apps add`: register one or more
// Android packages under the active Account, each with a cheap
// edits.insert+delete access probe (skippable via --no-verify) so typos
// and missing per-app permission grants are caught at registration time
// rather than weeks later in CI.
//
// The command is variadic (`apps add <pkg>...`, ADR-0040). Each package
// is an independent unit of work: a probe failure on one does not stop
// or roll back the others (partial success, not all-or-nothing). The two
// permissions differ — seeing an App via `apps accessible list`
// (Reporting scope, #347) and being able to `apps add` it
// (androidpublisher edits scope) are distinct grants — so a mixed batch
// where some packages register and others are refused is the *expected*
// bootstrap case, not a rare error. The exit code follows a
// non-retryable-wins rule so an agent driving the CLI does not blindly
// retry a batch that carries a permanent failure (see aggregateError).
package addcmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// validateTimeout caps the edits.insert+delete probe at a fixed
// budget. The whole point of validate-by-default is to fail fast at
// registration time; a stalled TCP connection or hung pool would
// otherwise let `gplay apps add` hang indefinitely in CI (until the
// job-level timeout fires, which can be minutes or hours later). The
// budget is per-package: variadic runs probe sequentially, so each probe
// gets the full window rather than sharing one across the batch.
const validateTimeout = 30 * time.Second

// Input is the request-shaped struct cobra builds from args + flags.
// Packages is the variadic <pkg>... argument list (ADR-0040); a
// single-element slice reproduces the pre-variadic contract byte-for-byte
// (same stderr line, same exit code, same --no-verify behavior).
type Input struct {
	Packages []string
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

// Run is the kernel-shaped business function. It resolves the Account
// once (a whole-command precondition — an unresolved credential aborts
// the batch before any package is touched), then processes each package
// independently: validate format (no HTTP), optionally probe the API,
// register in memory. The global config is saved ONCE at the end so a
// batch persists exactly the packages whose probe succeeded — a failure
// on one package neither blocks nor rolls back the others.
//
// A single-package invocation takes a strict non-regression path: it
// returns the raw per-package error (identical message and exit code to
// the pre-variadic command) and prints no ✗ line. Multi-package runs
// print a ✓/✗ line per package and, on any failure, return an
// aggregateError whose exit code follows the non-retryable-wins rule.
//
// The Account the packages are registered under is rc.AccountName — the
// Account that actually backs rc.Account, even when --account /
// GPLAY_ACCOUNT picked something other than the global active. The
// pre-fix code used rc.Resolved.ConfigAccount, which silently
// misattributed (Account, Package) pairs whenever a flag-layer or
// env-layer override beat the cascade.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkgs := dedup(in.Packages)
	if len(pkgs) == 0 {
		return nil, &usageError{msg: "apps add: at least one <package> argument is required"}
	}

	account, g, err := resolveAccount(rc)
	if err != nil {
		return nil, err
	}

	// Resolve the authenticated client once for the whole batch (probes
	// reuse it). Skipped entirely under --no-verify — hc stays nil and is
	// never dereferenced — so a purely offline registration never touches
	// the keyring or the network.
	var hc *http.Client
	if !in.NoVerify {
		hc, err = rc.AuthedClient()
		if err != nil {
			return nil, err
		}
	}

	// Single-package path: strict non-regression. Return the raw error so
	// the exit code and message are byte-for-byte what the pre-variadic
	// command produced; print no ✗ line.
	if len(pkgs) == 1 {
		if err := addOne(rc, hc, g, account, pkgs[0], in.NoVerify); err != nil {
			return nil, err
		}
		if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
			return nil, err
		}
		printAdded(rc, pkgs[0], account, in.NoVerify)
		return nil, nil
	}

	// Multi-package path: independent units, partial success.
	results := make([]pkgResult, 0, len(pkgs))
	addedAny := false
	for _, pkg := range pkgs {
		perr := addOne(rc, hc, g, account, pkg, in.NoVerify)
		results = append(results, pkgResult{pkg: pkg, err: perr})
		if perr == nil {
			addedAny = true
		}
	}

	// Persist the successful registrations. A Save failure here would lose
	// packages that already probed clean, so it aborts with the raw error —
	// but only after reporting the per-package outcomes, so the operator/
	// agent still sees which packages probed clean (already-computed
	// information that helps them retry a narrower set) rather than a bare
	// Save error. reportBatch runs exactly once on every path.
	if addedAny {
		if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
			reportBatch(rc, results, account, in.NoVerify)
			return nil, err
		}
	}

	reportBatch(rc, results, account, in.NoVerify)

	if agg := newAggregateError(results); agg != nil {
		return nil, agg
	}
	return nil, nil
}

// addOne runs the per-package pipeline: client-side format validation,
// then (unless NoVerify) the edits.insert+delete access probe, then the
// in-memory registry write. Order matters: format check first (no HTTP),
// API probe second, registry write last — so a failure at any step
// leaves g untouched for this package and the registry never carries a
// package the credential cannot reach. g is mutated in place on success;
// the caller saves once for the whole batch.
func addOne(rc *kernel.RunContext, hc *http.Client, g *config.Global, account, pkg string, noVerify bool) error {
	if err := validatePackage(pkg); err != nil {
		return err
	}
	if !noVerify {
		probeCtx, cancel := context.WithTimeout(rc.Ctx, validateTimeout)
		defer cancel()
		if err := edits.Validate(probeCtx, hc, pkg); err != nil {
			return err
		}
	}
	return registry.Add(&g.Accounts, account, pkg)
}

// resolveAccount performs the whole-command auth precondition and returns
// the Account name to register under plus the loaded global config. It
// reproduces the pre-variadic ordering exactly: empty AccountName is
// disambiguated (inline credential vs no credential), then the global
// config is loaded and the Account's presence asserted client-side so a
// keystore-only Account fails with a `gplay auth login` hint rather than
// a post-flight registry error.
func resolveAccount(rc *kernel.RunContext) (string, *config.Global, error) {
	if rc.AccountName == "" {
		// rc.AccountName is "" in two cases:
		//   1. No credential resolved at all (no flag, no env, no active in
		//      config) → user must `gplay auth login` or pass
		//      --account/--service-account.
		//   2. Credential came from --service-account or
		//      GPLAY_SERVICE_ACCOUNT (inline JSON, ad-hoc identity). Such
		//      credentials have no local Account name to register the package
		//      under; that path is unsupported for `apps add` because the
		//      registry is per-Account.
		// An empty name means the resolver can only reach an inline or
		// no-source layer, so EnsureAccount here is keystore-free — it never
		// probes for a stored Account.
		if err := rc.EnsureAccount(); err != nil {
			// A present-but-invalid inline credential (--service-account /
			// GPLAY_SERVICE_ACCOUNT) is a hard error (exit 10, the real cause)
			// instead of the misdirecting "run gplay auth login" below — never
			// register the package under a fallback Account (ADR-0020).
			return "", nil, err
		}
		if rc.Account != nil {
			return "", nil, &usageError{msg: "apps add: cannot register under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		return "", nil, &authError{msg: "no Account resolved; run `gplay auth login`, set GPLAY_ACCOUNT, or pass --account"}
	}
	account := rc.AccountName

	// Load the global config BEFORE the probe so a missing-Account registry
	// inconsistency is caught client-side. Otherwise the probe burns an HTTP
	// round-trip against Google only to fail post-flight on registry.Add
	// with ErrUnknownAccount — and the resulting "registry: unknown account"
	// message looks like CLI misuse when really it's a `gplay auth login`
	// workflow gap (the keystore has the credential but the global config
	// doesn't list the Account).
	g, err := config.LoadGlobalOrEmpty(rc.Ctx, fsOrDefault(rc), rc.ConfigPath)
	if err != nil {
		return "", nil, err
	}
	if !accountInGlobal(g, account) {
		return "", nil, &authError{msg: fmt.Sprintf(
			"apps add: Account %q is not in the global config (keystore-only Accounts are unsupported); run `gplay auth login` to register it",
			account,
		)}
	}
	return account, g, nil
}

// dedup removes duplicate package arguments, preserving first-seen order.
// `apps add a b a` probes and registers `a` once — the second mention is
// not a distinct unit of work and must not produce a second ✓/✗ line or a
// redundant probe.
func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// pkgResult pairs a package with the outcome of its unit of work (nil err
// = registered). The batch reporter and aggregateError both read it.
type pkgResult struct {
	pkg string
	err error
}

// successLine formats the "✓ registered ..." stderr line shared by the
// single-package path (printAdded) and the batch reporter (reportBatch),
// so the wording and the "(unverified)" qualifier cannot drift between
// them.
func successLine(pkg, account string, noVerify bool) string {
	verb := "registered"
	if noVerify {
		verb = "registered (unverified)"
	}
	return fmt.Sprintf("✓ %s %q under Account %q\n", verb, pkg, account)
}

// printAdded writes the single-package success line — byte-for-byte the
// pre-variadic stderr output.
func printAdded(rc *kernel.RunContext, pkg, account string, noVerify bool) {
	_, _ = fmt.Fprint(rc.Stderr, successLine(pkg, account, noVerify))
}

// reportBatch prints one line per package to stderr for a multi-package
// run: a ✓ for each registration and a ✗ (with the error and its exit
// code) for each failure, followed by a one-line tally. stderr is the
// only report channel — `apps add` has no --output (its result is a
// side effect on the local registry, not an API body), so this is what an
// operator or agent reads to see which packages landed.
func reportBatch(rc *kernel.RunContext, results []pkgResult, account string, noVerify bool) {
	if rc.Stderr == nil {
		return
	}
	ok, failed := 0, 0
	for _, r := range results {
		if r.err == nil {
			ok++
			_, _ = fmt.Fprint(rc.Stderr, successLine(r.pkg, account, noVerify))
			continue
		}
		failed++
		_, _ = fmt.Fprintf(rc.Stderr, "✗ %s: %s (exit %d)\n", r.pkg, r.err.Error(), exit.For(r.err))
	}
	_, _ = fmt.Fprintf(rc.Stderr, "apps add: %d registered, %d failed\n", ok, failed)
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

// NewCommand returns the cobra command for `gplay apps add <package>...`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "add <package>...",
		Short: "Register one or more Android packages under the active Account",
		Long: `Register one or more Android package names (e.g. com.example.myapp) under
the active Account in gplay's local registry. By default ` + "`apps add`" + `
validates access to each package by opening and immediately discarding a
Google Play Edit on it — a cheap probe that catches typos and missing
per-app permission grants at registration time rather than weeks later in
CI.

Multiple packages are independent units of work: a failure on one does not
stop or roll back the others (partial success). Each is reported with a ✓
or ✗ line on stderr; the exit code reflects the most serious failure, and
a batch that carries any non-retryable failure exits non-retryable so an
automated caller does not blindly retry it. Duplicate arguments are
collapsed. A single-package invocation behaves exactly as before.

Pass --no-verify to skip the API round-trip for every package (useful for
offline or preparatory registration).`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Packages: args, NoVerify: noVerify})
			})
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip the edits.insert+delete access probe for every package (record unconditionally)")
	return cmd
}
