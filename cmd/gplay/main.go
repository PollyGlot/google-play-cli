package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/apps/initcmd"
	"github.com/PollyGlot/google-play-cli/commands/auth/doctor"
	"github.com/PollyGlot/google-play-cli/commands/auth/list"
	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/commands/auth/logout"
	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// Build-time variables injected by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configDir, err := defaultConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gplay: %v\n", err)
		os.Exit(1)
	}
	boot := kernel.Boot{
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Stdin:        os.Stdin,
		ConfigPath:   filepath.Join(configDir, "config.json"),
		KeystoreRoot: filepath.Join(configDir, "accounts"),
		Keyring:      keystore.DefaultKeyring(),
	}

	if err := newRootCmd(boot).Execute(); err != nil {
		os.Exit(exit.For(err))
	}
}

func newRootCmd(boot kernel.Boot) *cobra.Command {
	root := &cobra.Command{
		Use:   "gplay",
		Short: "Google Play Developer CLI",
		Long: `gplay — fast, lightweight CLI for the Google Play Developer API.

Reads service-account credentials, mints OAuth2 tokens, and drives the
publishing surface (releases, tracks, reviews, vitals). Designed to
replace Fastlane on Android CI pipelines.`,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	// Persistent credential-resolution flags (docs/DESIGN.md §1). Every
	// subcommand inherits these via the cobra parent chain — login reads
	// the same --service-account everyone else does, so the contract stays
	// consistent across the binary.
	var (
		serviceAccountFlag string
		accountFlag        string
		verbose            bool
	)
	root.PersistentFlags().StringVar(&serviceAccountFlag, "service-account", "",
		"path to a service-account JSON, or inline JSON content (overrides --account, env, and active Account)")
	root.PersistentFlags().StringVar(&accountFlag, "account", "",
		"name of a stored Account to use (overrides env and active Account)")
	// Persistent verbosity flag (docs/DESIGN.md §8). Subcommands read it via
	// the inherited PersistentFlags so a single `-v` works at any position:
	//   gplay -v auth status   (CI-friendly: option before subcommand)
	//   gplay auth status -v   (interactive-friendly: option after)
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "log flow steps to stderr (info level)")

	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage gplay credentials",
	}
	auth.AddCommand(login.NewCommand(boot))
	auth.AddCommand(logout.NewCommand(boot))
	auth.AddCommand(status.NewCommand(boot))
	auth.AddCommand(list.NewCommand(boot))
	auth.AddCommand(doctor.NewCommand(boot))
	root.AddCommand(auth)

	// `gplay init` at the top level — pins a package to the current repo.
	// Also wired as `gplay apps init` once the apps subcommand exists.
	root.AddCommand(initcmd.NewCommand(initcmd.Options{}))

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print gplay version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "gplay %s (%s, %s)\n", version, commit, date)
		},
	})

	return root
}

// defaultConfigDir returns the canonical gplay config directory per the PRD:
//
//   - Linux:        $XDG_CONFIG_HOME/gplay (or ~/.config/gplay)
//   - macOS, Win:   ~/.gplay (deliberately NOT os.UserConfigDir's
//     ~/Library/Application Support or %AppData% — gplay sits next to
//     other dotfile-style dev tooling)
func defaultConfigDir() (string, error) {
	if runtime.GOOS == "linux" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "gplay"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gplay"), nil
}
