// Package logout implements `gplay auth logout <name>`: remove a
// registered Account from both the config and the keystore.
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
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// ErrUnknownAccount aliases config.ErrUnknownAccount so logout callers
// don't need to import both packages. exit.For maps it to exit code 2.
var ErrUnknownAccount = config.ErrUnknownAccount

// Input carries the positional Account name.
type Input struct {
	Name string
}

// Run mutates the registry + keystore. logout has no renderable output
// (free-form stderr line per docs/DESIGN.md §7), so it returns nil.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return nil, err
	}

	// Surface unknown-name errors before touching the keystore.
	if err := cfg.RemoveAccount(in.Name); err != nil {
		if errors.Is(err, config.ErrUnknownAccount) {
			_, _ = fmt.Fprintf(rc.Stderr,
				"unknown account %q. Known accounts: %s\n", in.Name, listAccountNames(cfg))
		}
		return nil, err
	}

	// keystore-not-found is tolerated: a prior logout may have
	// half-completed.
	if err := rc.Keystore.Delete(in.Name); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return nil, err
	}

	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q removed\n", in.Name)
	return nil, nil
}

// NewCommand returns the cobra command for `gplay auth logout <name>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout <name>",
		Short: "Remove a registered Account from the config and the keystore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			return kernel.Run(b, kernel.FromCobra(cmd, ""), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Name: args[0]})
			})
		},
	}
	return cmd
}

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
