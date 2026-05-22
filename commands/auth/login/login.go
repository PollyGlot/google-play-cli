// Package login implements `gplay auth login`: register a service-account
// JSON as a named Account in the keystore and mark it active in the config.
package login

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// ErrMissingServiceAccount is returned when login is invoked without a
// --service-account flag value. It satisfies exit.Coder so the binary
// exits 2 (CLI misuse) rather than the generic 1.
var ErrMissingServiceAccount = missingFlagError{}

type missingFlagError struct{}

func (missingFlagError) Error() string {
	return `login: --service-account is required (path to a service-account JSON, or inline JSON)`
}
func (missingFlagError) ExitCode() int { return 2 }

// Input is the business surface of `gplay auth login`.
type Input struct {
	// SAPath is the path to a service-account JSON file (or inline JSON
	// payload — login defers to serviceaccount.Load which accepts both).
	SAPath   string
	Name     string
	Activate bool
}

// Run is the pure business function. The keystore backend is resolved
// lazily on rc and the config is loaded directly from rc.ConfigPath.
func Run(rc *kernel.RunContext, in Input) error {
	if in.SAPath == "" {
		return ErrMissingServiceAccount
	}
	sa, err := serviceaccount.Load(in.SAPath)
	if err != nil {
		return err
	}
	name := in.Name
	if name == "" {
		name = deriveName(sa.ClientEmail)
	}

	be, _, err := rc.Backend()
	if err != nil {
		return err
	}
	if err := be.Save(name, sa.Raw); err != nil {
		return err
	}

	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return err
	}
	// Whether to set the new Account active. The exception (per #10): the
	// first registered Account always becomes active so the registry is
	// never left without one — otherwise `gplay auth login --activate=false`
	// on a fresh machine would leave the user with a non-functional setup.
	wasEmpty := len(cfg.Accounts) == 0
	cfg.AddAccount(name)
	if in.Activate || wasEmpty {
		if err := cfg.SetActive(name); err != nil {
			return err
		}
	}
	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return err
	}

	if in.Activate || wasEmpty {
		_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q registered and set active (%s)\n", name, sa.ClientEmail)
	} else {
		_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q registered (%s); active Account unchanged\n", name, sa.ClientEmail)
	}
	return nil
}

// NewCommand returns the cobra command for `gplay auth login`. The
// `--service-account` flag is inherited from the root command's
// persistent flags (docs/DESIGN.md §1) — login reads the same value
// every other command uses, so the contract stays consistent.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		name     string
		activate bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Register a service account as the active Account",
		Long: `Register a Google Cloud service account JSON as a named Account
in the local gplay registry and mark it active so subsequent commands use it
without an explicit --account flag.

The credential is stored in the OS keystore (macOS Keychain, Windows
Credential Manager, or Linux Secret Service). On systems without a keystore
daemon (headless Linux, CI containers), gplay transparently falls back to a
0600 file under the config directory. The active backend is reported by
` + "`gplay auth status`" + ` and logged once per process at -v.

Pass --activate=false to add a second Account without changing which one
is active. (The very first registered Account becomes active regardless,
so the registry is never left without one when --activate=false is set.)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --service-account is a root-level persistent flag (docs/DESIGN.md §1).
			saPath, _ := cmd.Flags().GetString("service-account")
			return Run(kernel.FromCobra(cmd, boot), Input{
				SAPath:   saPath,
				Name:     name,
				Activate: activate,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "friendly Account name (default: derived from client_email)")
	cmd.Flags().BoolVar(&activate, "activate", true, "mark the new Account active (default true)")
	return cmd
}

// deriveName returns the left-of-@ part of a service-account email. Returns
// the input unchanged when no '@' is present.
func deriveName(email string) string {
	local, _, _ := strings.Cut(email, "@")
	return local
}
