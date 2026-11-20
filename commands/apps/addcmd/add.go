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

const validateTimeout = 30 * time.Second

type Input struct {
	Packages []string
	NoVerify bool
}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkgs := dedup(in.Packages)
	if len(pkgs) == 0 {
		return nil, &exit.UsageError{Msg: "apps add: at least one <package> argument is required"}
	}

	account, g, err := resolveAccount(rc)
	if err != nil {
		return nil, err
	}

	var hc *http.Client
	if !in.NoVerify {
		hc, err = rc.AuthedClient()
		if err != nil {
			return nil, err
		}
	}

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

	results := make([]pkgResult, 0, len(pkgs))
	addedAny := false
	for _, pkg := range pkgs {
		perr := addOne(rc, hc, g, account, pkg, in.NoVerify)
		results = append(results, pkgResult{pkg: pkg, err: perr})
		if perr == nil {
			addedAny = true
		}
	}

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

func resolveAccount(rc *kernel.RunContext) (string, *config.Global, error) {
	if rc.AccountName == "" {
		if err := rc.EnsureAccount(); err != nil {
			return "", nil, err
		}
		if rc.Account != nil {
			return "", nil, &exit.UsageError{Msg: "apps add: cannot register under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		return "", nil, kernel.NoAccountError()
	}
	account := rc.AccountName

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

type pkgResult struct {
	pkg string
	err error
}

func successLine(pkg, account string, noVerify bool) string {
	verb := "registered"
	if noVerify {
		verb = "registered (unverified)"
	}
	return fmt.Sprintf("%s %q under Account %q", verb, pkg, account)
}

func printAdded(rc *kernel.RunContext, pkg, account string, noVerify bool) {
	rc.Confirmf("%s", successLine(pkg, account, noVerify))
}

func reportBatch(rc *kernel.RunContext, results []pkgResult, account string, noVerify bool) {
	ok, failed := 0, 0
	for _, r := range results {
		if r.err == nil {
			ok++
			rc.Confirmf("%s", successLine(r.pkg, account, noVerify))
			continue
		}
		failed++
		rc.Failf("✗ %s: %s (exit %d)", r.pkg, r.err.Error(), exit.For(r.err))
	}
	rc.Logf("apps add: %d registered, %d failed", ok, failed)
}

func validatePackage(pkg string) error {
	if pkg == "" {
		return &exit.UsageError{Msg: "apps add: <package> argument is required"}
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

func NewCommand(boot kernel.Boot) *cobra.Command {
	var noVerify bool
	cmd := &cobra.Command{
		Use:   "add <package>...",
		Short: "Register one or more Android packages under the active Account",
		Long: `Register one or more Android package names (e.g. com.example.myapp) under
the active Account in gplay's local registry. By default ` + "`apps add`" + `
validates access to each package by opening and immediately discarding a
Google Play Edit on it: a cheap probe that catches typos and missing
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
		Example: `  # Register an app, checking access with a throwaway Edit
  gplay apps add com.example.app

  # Register several at once (each succeeds or fails on its own)
  gplay apps add com.example.app com.example.lite com.example.wear

  # Record without any API call, e.g. before the credential has access
  gplay apps add com.example.app --no-verify`,
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
