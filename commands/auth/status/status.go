// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, the keystore backend in use, and
// (when applicable) the on-disk credential path. JSON output is supported
// via `--output json`.
package status

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options pins where the command reads state. Output streams are wired via
// cobra's SetOut/SetErr (with os.Stdout/os.Stderr as the default).
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	// Keyring is the keystore backend the command will probe. If nil, the
	// default go-keyring adapter is used.
	Keyring keystore.KeyringAPI
}

// NewCommand returns the cobra command for `gplay auth status`.
func NewCommand(opts Options) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account, the keystore backend, and where the credential lives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Verbose is a root-level persistent flag (docs/DESIGN.md §8). We
			// read it via the inherited flag set so `-v` works at any
			// position on the command line.
			verbose, _ := cmd.Flags().GetBool("verbose")
			return run(cmd, opts, output, verbose)
		},
	}
	cmd.Flags().StringVar(&output, "output", "table", "output format: table or json")
	return cmd
}

type payload struct {
	Name        string `json:"name"`
	ClientEmail string `json:"client_email"`
	Backend     string `json:"backend"`
	Path        string `json:"path"`
}

func run(cmd *cobra.Command, opts Options, output string, verbose bool) error {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
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

	sa, err := resolver.New(cfg, be).Resolve(resolver.Inputs{})
	if err != nil {
		return err
	}
	active, _ := cfg.Active()

	p := payload{
		Name:        active.Name,
		ClientEmail: sa.ClientEmail,
		Backend:     label,
	}
	if label == "file" {
		p.Path = filepath.Join(opts.KeystoreRoot, active.Name+".json")
	}

	stdout := cmd.OutOrStdout()
	switch output {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case "table":
		if _, err := fmt.Fprintf(stdout, "Active account: %s\nClient email:   %s\n",
			p.Name, p.ClientEmail); err != nil {
			return err
		}
		if p.Backend == "keyring" {
			_, err := fmt.Fprintf(stdout, "Backend:        keystore\n")
			return err
		}
		_, err := fmt.Fprintf(stdout, "Backend:        file: %s\n", p.Path)
		return err
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}
