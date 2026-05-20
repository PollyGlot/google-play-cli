// Package login implements `gplay auth login`: register a service-account
// JSON as a named Account in the keystore and mark it active in the config.
package login

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options pins where the command reads and writes state. Output streams are
// not part of Options — cobra's SetOut/SetErr (with os.Stdout/os.Stderr as
// the default) is the canonical wiring.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	// Keyring is the keystore backend the command will probe. If nil, the
	// default go-keyring adapter is used.
	Keyring keystore.KeyringAPI
}

// NewCommand returns the cobra command for `gplay auth login`.
func NewCommand(opts Options) *cobra.Command {
	var (
		saPath   string
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
			// Verbose is a root-level persistent flag (docs/DESIGN.md §8).
			verbose, _ := cmd.Flags().GetBool("verbose")
			return run(cmd, opts, saPath, name, activate, verbose)
		},
	}
	cmd.Flags().StringVar(&saPath, "service-account", "", "path to a Google Cloud service-account JSON (required)")
	cmd.Flags().StringVar(&name, "name", "", "friendly Account name (default: derived from client_email)")
	cmd.Flags().BoolVar(&activate, "activate", true, "mark the new Account active (default true)")
	_ = cmd.MarkFlagRequired("service-account")
	return cmd
}

func run(cmd *cobra.Command, opts Options, saPath, name string, activate, verbose bool) error {
	sa, err := serviceaccount.Load(saPath)
	if err != nil {
		return err
	}
	if name == "" {
		name = deriveName(sa.ClientEmail)
	}

	kr := opts.Keyring
	if kr == nil {
		kr = keystore.DefaultKeyring()
	}
	be, label, err := keystore.Select(keystore.SelectOptions{
		Keyring:  kr,
		FileRoot: opts.KeystoreRoot,
	})
	if err != nil {
		return err
	}
	if verbose {
		keystore.LogBackendOnce(cmd.ErrOrStderr(), label)
	}
	if err := be.Save(name, sa.Raw); err != nil {
		return err
	}

	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	// Whether to set the new Account active. The exception (per #10): the
	// first registered Account always becomes active so the registry is
	// never left without one — otherwise `gplay auth login --activate=false`
	// on a fresh machine would leave the user with a non-functional setup.
	wasEmpty := len(cfg.Accounts) == 0
	cfg.AddAccount(name)
	if activate || wasEmpty {
		if err := cfg.SetActive(name); err != nil {
			return err
		}
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		return err
	}

	if activate || wasEmpty {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ Account %q registered and set active (%s)\n", name, sa.ClientEmail)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ Account %q registered (%s); active Account unchanged\n", name, sa.ClientEmail)
	}
	return nil
}

// deriveName returns the left-of-@ part of a service-account email. Returns
// the input unchanged when no '@' is present.
func deriveName(email string) string {
	local, _, _ := strings.Cut(email, "@")
	return local
}
