package removecmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apps/registry"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type Input struct {
	Package string
}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if err := validatePackage(in.Package); err != nil {
		return nil, err
	}
	if rc.AccountName == "" {
		if err := rc.EnsureAccount(); err != nil {
			return nil, err
		}
		if rc.Account != nil {
			return nil, &exit.UsageError{Msg: "apps remove: cannot remove under an inline credential (--service-account / GPLAY_SERVICE_ACCOUNT); first `gplay auth login` then re-run with --account <name>"}
		}
		return nil, kernel.NoAccountError()
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
	if !registry.Has(g.Accounts, rc.AccountName, in.Package) {
		rc.Logf("%q not in registry, nothing to do", in.Package)
		return nil, nil
	}
	_ = registry.Remove(&g.Accounts, rc.AccountName, in.Package)
	if err := g.Save(rc.Ctx, fsOrDefault(rc), rc.ConfigPath); err != nil {
		return nil, err
	}
	rc.Confirmf("removed %q from Account %q", in.Package, rc.AccountName)
	if rc.Resolved != nil && rc.Resolved.Pin == in.Package && rc.Resolved.ProjectSharedPath != "" {
		rc.Warnf(".gplay/config.json still pins %q; run `gplay init --package <other>` to repin or edit the file manually",
			in.Package,
		)
	}
	return nil, nil
}

func validatePackage(pkg string) error {
	if pkg == "" {
		return &exit.UsageError{Msg: "apps remove: <package> argument is required"}
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

func accountInGlobal(g *config.Global, name string) bool {
	for _, a := range g.Accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

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
		Example: `  # Stop tracking an app locally (Google Play is not touched)
  gplay apps remove com.example.old`,
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
