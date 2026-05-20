// Package logout implements `gplay auth logout <name>`: remove a
// registered Account from both the config and the keystore.
//
// If the removed Account was the active one, the registry is left with
// no active Account — callers can then run `gplay auth login` or
// (future) `gplay auth use <name>` to set a new active. This is
// intentional: choosing a new active silently would surprise users who
// just removed the wrong one.
package logout

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options pins where the command reads and writes state.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	// Keyring is the keystore backend the command will probe. If nil, the
	// default go-keyring adapter is used. Tests inject a fake.
	Keyring keystore.KeyringAPI
}

// ErrUnknownAccount is returned when the user asks to log out of an
// Account that is not registered. The command layer maps this to
// exit code 2 (CLI misuse) and prints the list of known names.
var ErrUnknownAccount = errors.New("logout: unknown account")

// NewCommand returns the cobra command for `gplay auth logout <name>`.
func NewCommand(opts Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout <name>",
		Short: "Remove a registered Account from the config and the keystore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose, _ := cmd.Flags().GetBool("verbose")
			return run(cmd, opts, args[0], verbose)
		},
	}
	return cmd
}

func run(cmd *cobra.Command, opts Options, name string, verbose bool) error {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}

	// Validate before touching anything so a typo doesn't half-delete state.
	if !hasAccount(cfg, name) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"unknown account %q. Known accounts: %s\n", name, listAccountNames(cfg))
		return ErrUnknownAccount
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

	// Order matters: delete the credential first so a config-write failure
	// after credential deletion still leaves a consistent "credential gone"
	// state. The orphan config entry would just be a stale name.
	if err := be.Delete(name); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return err
	}

	if err := cfg.RemoveAccount(name); err != nil {
		return err
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "✓ Account %q removed\n", name)
	return nil
}

func hasAccount(cfg *config.Config, name string) bool {
	for _, a := range cfg.Accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

func listAccountNames(cfg *config.Config) string {
	if len(cfg.Accounts) == 0 {
		return "(none registered)"
	}
	names := make([]string, len(cfg.Accounts))
	for i, a := range cfg.Accounts {
		names[i] = a.Name
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
