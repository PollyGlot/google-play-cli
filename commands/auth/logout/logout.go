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
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// ErrUnknownAccount aliases config.ErrUnknownAccount so logout callers
// don't need to import both packages. exit.For maps it to exit code 2.
var ErrUnknownAccount = config.ErrUnknownAccount

// confirmRequired builds the refusal returned when logout runs without
// --confirm. It is an *exit.SafetyFlagError, so it exits 3: "safety flag
// required" per docs/DESIGN.md §9, and names the missing flag in the
// --output json error envelope's requires[] (ADR-0017 / ADR-0023). The
// destructive-op contract requires an explicit opt-in; the invocation
// itself is well-formed, which is why this is not the exit-2 usage error.
func confirmRequired() error {
	return exit.SafetyFlag("confirm", "logout: --confirm is required to remove a credential (this operation is destructive)")
}

// Input carries the positional Account name and the --confirm opt-in.
type Input struct {
	Name    string
	Confirm bool
}

// Run mutates the registry + keystore. logout has no renderable output
// (free-form stderr line per docs/DESIGN.md §7), so it returns nil.
// Refuses to run without Input.Confirm: the destructive-op contract.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.Confirm {
		return nil, confirmRequired()
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

	// The backend is selected here (not at boot) so a logout that fails
	// the --confirm gate above never probes the keyring.
	be, err := rc.Backend()
	if err != nil {
		return nil, err
	}
	removed, err := deleteEverywhere(rc, be, in.Name)
	if err != nil {
		return nil, err
	}
	if len(removed) == 0 {
		if fb, ok := be.(*keystore.FileBackend); ok {
			return nil, keyringUnreachableError(in.Name, fb)
		}
		// Both stores were reachable and neither holds the key: it is
		// provably gone (a half-finished earlier logout, a keychain wiped by
		// hand). Stay idempotent: succeed, but do not claim a deletion.
		_, _ = fmt.Fprintf(rc.Stderr, "warning: no stored credential found, nothing to delete (Account %q)\n", in.Name)
		_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q removed from the registry\n", in.Name)
		return nil, nil
	}

	_, _ = fmt.Fprintf(rc.Stderr, "✓ Account %q removed (credential deleted from %s)\n", in.Name, strings.Join(removed, " and "))
	return nil, nil
}

// deleteEverywhere removes name from the selected backend AND from the file
// backend. Select picks the backend per process, so a key written to the
// plaintext file by a login that could not reach the keyring (SSH to a Mac
// with a locked keychain) is invisible to a later logout that can: deleting
// only from the selected backend would report success and leave the private
// key on disk. It returns the stores that actually held the credential;
// ErrNotFound from either is not an error, anything else is.
func deleteEverywhere(rc *kernel.RunContext, be keystore.Backend, name string) ([]string, error) {
	var removed []string
	selected, isFile := be.(*keystore.FileBackend)
	if err := be.Delete(rc.Ctx, name); err == nil {
		if isFile {
			removed = append(removed, selected.Path(name))
		} else {
			removed = append(removed, "the OS keyring")
		}
	} else if !errors.Is(err, keystore.ErrNotFound) {
		return nil, err
	}
	if isFile {
		return removed, nil
	}
	file := keystore.NewFileBackend(rc.KeystoreRoot)
	if err := file.Delete(rc.Ctx, name); err == nil {
		removed = append(removed, file.Path(name))
	} else if !errors.Is(err, keystore.ErrNotFound) {
		return nil, err
	}
	return removed, nil
}

// keyringUnreachableError reports a logout that removed the registry entry
// but found no file credential while the keyring could not be reached: the
// key may still sit in the keyring, so gplay cannot say it is gone. It fails
// rather than printing "removed" because the user runs logout to know the key
// is gone.
func keyringUnreachableError(name string, fb *keystore.FileBackend) error {
	return fmt.Errorf("logout: Account %q removed from the registry, but no credential was found at %s and the OS keyring is unavailable: if it was stored there it was NOT deleted; remove it from the OS keyring (service %q) by hand", name, fb.Path(name), keystore.KeyringService)
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
