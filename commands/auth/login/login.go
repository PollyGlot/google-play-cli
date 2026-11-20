package login

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

var ErrMissingServiceAccount = missingFlagError{}

type missingFlagError struct{}

func (missingFlagError) Error() string {
	return `login: --service-account is required (path to a service-account JSON, or inline JSON)`
}
func (missingFlagError) ExitCode() int { return 2 }

type Input struct {
	SAPath      string
	Name        string
	Activate    bool
	DeveloperID string
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.SAPath == "" {
		return nil, ErrMissingServiceAccount
	}
	sa, err := serviceaccount.LoadValue(rc.FS, "--service-account", in.SAPath)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = deriveName(sa.ClientEmail)
	}

	be, err := rc.Backend()
	if err != nil {
		return nil, err
	}
	if err := be.Save(rc.Ctx, name, sa.Raw); err != nil {
		return nil, err
	}
	if fb, ok := be.(*keystore.FileBackend); ok {
		keystore.WarnFileFallback(rc.Stderr, fb.Path(name))
	}

	cfg, err := config.LoadGlobalOrEmpty(rc.Ctx, rc.FS, rc.ConfigPath)
	if err != nil {
		return nil, err
	}
	wasEmpty := len(cfg.Accounts) == 0
	cfg.AddAccount(name)
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
		rc.Confirmf("Account %q registered and set active (%s)", name, sa.ClientEmail)
	} else {
		rc.Confirmf("Account %q registered (%s); active Account unchanged", name, sa.ClientEmail)
	}
	if devID != "" {
		rc.Logf("  developer-id %s recorded for `gplay team`", devID)
	}
	return nil, nil
}

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
without an explicit --account flag. The JSON comes from the global
--service-account flag: a file path, or the JSON content itself. In CI, skip
login: expose the JSON as GPLAY_SERVICE_ACCOUNT instead.

The credential is stored in the OS keystore (macOS Keychain, Windows
Credential Manager, or Linux Secret Service). On systems without a keystore
daemon (headless Linux, CI containers, a locked macOS keychain over SSH),
gplay falls back to a plaintext 0600 file under the config directory and
prints a warning naming that file. The active backend is reported by
` + "`gplay auth status`" + `.

Pass --activate=false to add a second Account without changing which one
is active. (The very first registered Account becomes active regardless,
so the registry is never left without one when --activate=false is set.)`,
		Example: `  # Register a service account key file and make it the active Account
  gplay auth login --service-account ./play-sa.json

  # Same, under a name of your choice
  gplay auth login --service-account ./play-sa.json --name release-bot

  # Add a second Account for the team commands without switching to it
  gplay auth login --service-account ./admin-sa.json --name admin \
    --developer-id 1234567890123456789 --activate=false`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			saPath, _ := cmd.Flags().GetString("service-account")
			return kernel.RunCobra(cmd, boot, "", func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{SAPath: saPath, Name: name, Activate: activate, DeveloperID: developerID})
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "friendly Account name (default: derived from client_email)")
	cmd.Flags().BoolVar(&activate, "activate", true, "mark the new Account active")
	cmd.Flags().StringVar(&developerID, "developer-id", "", "Play Console Developer account id to record on this Account (used by the team commands)")
	return cmd
}

func deriveName(email string) string {
	local, _, _ := strings.Cut(email, "@")
	return local
}
