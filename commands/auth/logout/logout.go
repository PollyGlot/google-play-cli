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
	"strings"

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

// ErrUnknownAccount is the sentinel surfaced when the requested Account
// is not registered. It aliases the canonical error from
// internal/config so callers don't have to import both packages just to
// check the error type. The command layer maps this to exit code 2.
var ErrUnknownAccount = config.ErrUnknownAccount

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

	// We delete from the in-memory config first to surface unknown-name
	// errors before touching the keystore. RemoveAccount returns
	// config.ErrUnknownAccount if the name is not registered, which the
	// command layer maps to exit 2.
	if err := cfg.RemoveAccount(name); err != nil {
		if errors.Is(err, config.ErrUnknownAccount) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"unknown account %q. Known accounts: %s\n", name, listAccountNames(cfg))
		}
		return err
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

	// Delete the credential after the config edit succeeded in memory.
	// A keystore-not-found is tolerated: the credential may already be
	// gone if a prior logout half-completed.
	if err := be.Delete(name); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return err
	}

	if err := cfg.Save(opts.ConfigPath); err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "✓ Account %q removed\n", name)
	return nil
}

// listAccountNames returns a sorted, comma-separated rendering of the
// registered Account names, or the placeholder when the registry is
// empty. Used in the unknown-name error hint.
func listAccountNames(cfg *config.Config) string {
	if len(cfg.Accounts) == 0 {
		return "(none registered)"
	}
	names := make([]string, len(cfg.Accounts))
	for i, a := range cfg.Accounts {
		names[i] = a.Name
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
