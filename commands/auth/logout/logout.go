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

// ErrConfirmRequired is returned when logout runs without --confirm.
// Mapping to exit code 2 (CLI misuse) per docs/DESIGN.md §9; the
// destructive-op contract requires an explicit opt-in.
var ErrConfirmRequired = confirmError{}

type confirmError struct{}

func (confirmError) Error() string {
	return "logout: --confirm is required to remove a credential (this operation is destructive)"
}
func (confirmError) ExitCode() int { return 2 }

// Input carries the positional Account name and the --confirm opt-in.
type Input struct {
	Name    string
	Confirm bool
}

// Run mutates the registry + keystore. logout has no renderable output
// (free-form stderr line per docs/DESIGN.md §7), so it returns nil.
// Refuses to run without Input.Confirm — the destructive-op contract.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.Confirm {
		return nil, ErrConfirmRequired
	}
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

	// Save config FIRST so a Delete-then-Save-fail leaves the registry
	// intact (next invocation can re-attempt the keystore cleanup);
	// the reverse order would leave config pointing at a credential
	// whose bytes are gone.
	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return nil, err
	}

	// keystore-not-found is tolerated: a prior logout may have
	// half-completed, or the user wiped the keychain by hand.
	if err := rc.Keystore.Delete(rc.Ctx, in.Name); err != nil && !errors.Is(err, keystore.ErrNotFound) {
		return nil, err
	}

	_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q removed\n", in.Name)
	return nil, nil
}

// NewCommand returns the cobra command for `gplay auth logout <name>`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "logout <name>",
		Short: "Remove a registered Account from the config and the keystore",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Name: args[0], Confirm: confirm})
			})
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm credential removal (required; see docs/DESIGN.md §9)")
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
