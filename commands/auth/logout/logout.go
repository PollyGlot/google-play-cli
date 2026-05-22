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
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// ErrUnknownAccount is the sentinel surfaced when the requested Account
// is not registered. It aliases the canonical error from
// internal/config so callers don't have to import both packages just to
// check the error type. exit.For maps this to exit code 2.
var ErrUnknownAccount = config.ErrUnknownAccount

// Input is the business surface of `gplay auth logout`.
type Input struct {
	Name string
}

// Run is the pure business function: load config, remove the account,
// delete from the keystore, save config. No cobra dependency.
func Run(rc *kernel.RunContext, in Input) error {
	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return err
	}

	// We delete from the in-memory config first to surface unknown-name
	// errors before touching the keystore. RemoveAccount returns
	// config.ErrUnknownAccount if the name is not registered, which
	// exit.For maps to exit code 2.
	if err := cfg.RemoveAccount(in.Name); err != nil {
		if errors.Is(err, config.ErrUnknownAccount) {
			_, _ = fmt.Fprintf(rc.Stderr,
				"unknown account %q. Known accounts: %s\n", in.Name, listAccountNames(cfg))
		}
		return err
	}

	be, _, err := rc.Backend()
	if err != nil {
		return err
	}

	// Delete the credential after the config edit succeeded in memory.
	// A keystore-not-found is tolerated: the credential may already be
	// gone if a prior logout half-completed.
	if err := be.Delete(in.Name); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return err
	}

	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q removed\n", in.Name)
	return nil
}

// NewCommand returns the cobra command for `gplay auth logout <name>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout <name>",
		Short: "Remove a registered Account from the config and the keystore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(kernel.FromCobra(cmd, boot), Input{Name: args[0]})
		},
	}
	return cmd
}

// listAccountNames returns a sorted, comma-separated rendering of the
// registered Account names, or the placeholder when the registry is
// empty. Used in the unknown-name error hint.
func listAccountNames(cfg *config.Global) string {
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
