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

var ErrUnknownAccount = config.ErrUnknownAccount

func confirmRequired() error {
	return exit.SafetyFlag("confirm", "logout: --confirm is required to remove a credential (this operation is destructive)")
}

type Input struct {
	Name    string
	Confirm bool
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if !in.Confirm {
		return nil, confirmRequired()
	}
	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return nil, err
	}

	if err := cfg.RemoveAccount(in.Name); err != nil {
		if errors.Is(err, config.ErrUnknownAccount) {
			rc.Failf("unknown account %q. Known accounts: %s", in.Name, listAccountNames(cfg))
		}
		return nil, err
	}

	if err := cfg.Save(rc.Ctx, rc.FS, rc.ConfigPath); err != nil {
		return nil, err
	}

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
		rc.Warnf("no stored credential found, nothing to delete (Account %q)", in.Name)
		rc.Confirmf("Account %q removed from the registry", in.Name)
		return nil, nil
	}

	rc.Confirmf("Account %q removed (credential deleted from %s)", in.Name, strings.Join(removed, " and "))
	return nil, nil
}

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

func keyringUnreachableError(name string, fb *keystore.FileBackend) error {
	return fmt.Errorf("logout: Account %q removed from the registry, but no credential was found at %s and the OS keyring is unavailable: if it was stored there it was NOT deleted; remove it from the OS keyring (service %q) by hand", name, fb.Path(name), keystore.KeyringService)
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "logout <name>",
		Short: "Remove a registered Account from the config and the keystore",
		Example: `  # Remove the Account named release-bot and delete its credential from the keystore
  gplay auth logout release-bot --confirm`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{Name: args[0], Confirm: confirm})
			})
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm credential removal (required: the credential is deleted from the keystore; without it logout exits 3)")
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
