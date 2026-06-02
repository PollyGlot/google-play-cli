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
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// ErrMissingServiceAccount is returned when login runs without a
// --service-account flag. It satisfies exit.Coder so the binary exits
// 2 (CLI misuse) rather than the generic 1.
var ErrMissingServiceAccount = missingFlagError{}

type missingFlagError struct{}

func (missingFlagError) Error() string {
	return `login: --service-account is required (path to a service-account JSON, or inline JSON)`
}
func (missingFlagError) ExitCode() int { return 2 }

// Input carries login-specific flags. SAPath comes from the root
// persistent --service-account flag (docs/DESIGN.md §1); the kernel
// hands it through via rc.* — but since the flag is captured at the
// cobra wrapper level, the closure passes it explicitly here.
type Input struct {
	SAPath      string
	Name        string
	Activate    bool
	DeveloperID string
}

// Run registers the named Account in keystore + config. login emits a
// free-form stderr line and returns no Renderable. The service-account
// JSON is read through rc.FS so tests can drive login with an in-memory
// fixture and Ctrl-C propagates through rc.Ctx.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.SAPath == "" {
		return nil, ErrMissingServiceAccount
	}
	sa, err := serviceaccount.LoadFromFS(rc.Ctx, rc.FS, in.SAPath)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = deriveName(sa.ClientEmail)
	}

	if err := rc.Keystore.Save(rc.Ctx, name, sa.Raw); err != nil {
		return nil, err
	}

	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return nil, err
	}
	// First Account always activates so the registry is never empty.
	wasEmpty := len(cfg.Accounts) == 0
	cfg.AddAccount(name)
	// Record the Developer account id on this Account if supplied — the
	// explicit capture point for `gplay team` addressing (ADR-0015 / #151).
	// login is explicit, so a re-login with --developer-id updates it.
	devID := strings.TrimSpace(in.DeveloperID)
	if devID != "" {
		cfg.SetDeveloperID(name, devID)
	}
	if in.Activate || wasEmpty {
		if err := cfg.SetActive(name); err != nil {
			return nil, err
		}
	}
	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return nil, err
	}

	if in.Activate || wasEmpty {
		_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q registered and set active (%s)\n", name, sa.ClientEmail)
	} else {
		_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q registered (%s); active Account unchanged\n", name, sa.ClientEmail)
	}
	if devID != "" {
		_, _ = fmt.Fprintf(rc.Stderr, "  developer-id %s recorded for `gplay team`\n", devID)
	}
	return nil, nil
}

// NewCommand returns the cobra command for `gplay auth login`. The
// --service-account flag is inherited from the root command's
// persistent flags.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		name        string
		activate    bool
		developerID string
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
			saPath, _ := cmd.Flags().GetString("service-account")
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{SAPath: saPath, Name: name, Activate: activate, DeveloperID: developerID})
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "friendly Account name (default: derived from client_email)")
	cmd.Flags().BoolVar(&activate, "activate", true, "mark the new Account active (default true)")
	cmd.Flags().StringVar(&developerID, "developer-id", "", "Play Console Developer account id to record on this Account (for `gplay team`)")
	return cmd
}

func deriveName(email string) string {
	local, _, _ := strings.Cut(email, "@")
	return local
}
