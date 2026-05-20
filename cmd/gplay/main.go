package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/auth/login"
	"github.com/PollyGlot/google-play-cli/commands/auth/status"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
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
	rootOpts := rootOptions{
		ConfigPath:   filepath.Join(configDir, "config.json"),
		KeystoreRoot: filepath.Join(configDir, "accounts"),
	}

	if err := newRootCmd(rootOpts).Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

type rootOptions struct {
	ConfigPath   string
	KeystoreRoot string
}

func newRootCmd(opts rootOptions) *cobra.Command {
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
	// subcommand inherits these; subcommands that already declare a local
	// --service-account flag (e.g. `auth login`) shadow the persistent one
	// — that is intentional and harmless because `auth login` always uses
	// the local value directly.
	var (
		serviceAccountFlag string
		accountFlag        string
	)
	root.PersistentFlags().StringVar(&serviceAccountFlag, "service-account", "",
		"path to a service-account JSON, or inline JSON content (overrides --account, env, and active Account)")
	root.PersistentFlags().StringVar(&accountFlag, "account", "",
		"name of a stored Account to use (overrides env and active Account)")

	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage gplay credentials",
	}
	auth.AddCommand(login.NewCommand(login.Options{
		ConfigPath:   opts.ConfigPath,
		KeystoreRoot: opts.KeystoreRoot,
	}))
	auth.AddCommand(status.NewCommand(status.Options{
		ConfigPath:   opts.ConfigPath,
		KeystoreRoot: opts.KeystoreRoot,
	}))
	root.AddCommand(auth)

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

// exitCode maps the typed errors raised by the auth modules to the semantic
// exit codes documented in docs/DESIGN.md §9.
func exitCode(err error) int {
	if err == nil {
		return 0
	}

	var mfe *serviceaccount.MissingFieldError
	if errors.As(err, &mfe) {
		return 10
	}
	var ae *token.AuthError
	if errors.As(err, &ae) {
		return 10
	}
	if errors.Is(err, resolver.ErrNoSource) {
		return 10
	}
	return 1
}
