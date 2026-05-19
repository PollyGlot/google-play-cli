// Package login implements `gplay auth login`: register a service-account
// JSON as a named Account in the keystore and mark it active in the config.
package login

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options configures where the command reads and writes state, and where it
// renders output. main.go fills these in from os.UserConfigDir; tests pass
// t.TempDir paths.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	Stdout       io.Writer
	Stderr       io.Writer
}

// NewCommand returns the cobra command for `gplay auth login`.
func NewCommand(opts Options) *cobra.Command {
	var (
		saPath string
		name   string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Register a service account as the active Account",
		Long: `Register a Google Cloud service account JSON as a named Account
in the local gplay registry and mark it active so subsequent commands use it
without an explicit --account flag.

The credential is stored under the active keystore backend (file-only in
this MVP slice; OS keystores arrive in a follow-up).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(opts, saPath, name)
		},
	}
	cmd.Flags().StringVar(&saPath, "service-account", "", "path to a Google Cloud service-account JSON (required)")
	cmd.Flags().StringVar(&name, "name", "", "friendly Account name (default: derived from client_email)")
	_ = cmd.MarkFlagRequired("service-account")

	if opts.Stdout != nil {
		cmd.SetOut(opts.Stdout)
	}
	if opts.Stderr != nil {
		cmd.SetErr(opts.Stderr)
	}
	return cmd
}

func run(opts Options, saPath, name string) error {
	sa, err := serviceaccount.Load(saPath)
	if err != nil {
		return err
	}
	if name == "" {
		name = deriveName(sa.ClientEmail)
	}

	be := keystore.NewFileBackend(opts.KeystoreRoot)
	if err := be.Save(name, sa.Raw); err != nil {
		return err
	}

	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg.AddAccount(name)
	if err := cfg.SetActive(name); err != nil {
		return err
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		return err
	}

	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	fmt.Fprintf(stderr, "✓ Account %q registered and set active (%s)\n", name, sa.ClientEmail)
	return nil
}

// deriveName returns the left-of-@ part of a service-account email, suitable
// as a friendly Account name. Empty string passes through.
func deriveName(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}
