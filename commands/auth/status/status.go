// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, the keystore backend in use, and
// (when applicable) the on-disk credential path. JSON output is supported
// via `--output json`.
//
// When no credential resolves (no active Account, no env override),
// status prints a friendly "no active account" line and exits 0 — it is
// informational, not a hard error.
package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Active      bool   `json:"active"`
	Name        string `json:"name,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Path        string `json:"path,omitempty"`
}

func run(cmd *cobra.Command, opts Options, output string, verbose bool) error {
	resolved, err := config.LoadFromEnv(opts.ConfigPath)
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

	sa, err := resolver.New(resolved, be).Resolve(resolver.Inputs{})
	if err != nil {
		if errors.Is(err, resolver.ErrNoSource) {
			return renderEmpty(cmd.OutOrStdout(), output)
		}
		return err
	}
	activeName := resolved.ConfigAccount

	p := payload{
		Active:      true,
		Name:        activeName,
		ClientEmail: sa.ClientEmail,
		Backend:     label,
	}
	if label == keystore.BackendFile {
		p.Path = filepath.Join(opts.KeystoreRoot, activeName+".json")
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
		if p.Backend == keystore.BackendKeyring {
			_, err := fmt.Fprintf(stdout, "Backend:        %s\n", keystore.BackendKeyring)
			return err
		}
		_, err := fmt.Fprintf(stdout, "Backend:        %s: %s\n", keystore.BackendFile, p.Path)
		return err
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}

// renderEmpty prints the informational "no active account" payload when
// no credential resolves. Exit code stays 0 — this is state, not failure.
func renderEmpty(w io.Writer, output string) error {
	switch output {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(payload{Active: false})
	case "table":
		_, err := fmt.Fprintln(w, "No active account.")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, "Run `gplay auth login` to register one, or `gplay auth list` to see registered Accounts.")
		return err
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}
