// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, and where the credential is stored.
// JSON output is supported via `--output json`.
package status

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
)

// Options injects state locations and writers so tests stay isolated.
type Options struct {
	ConfigPath   string
	KeystoreRoot string
	Stdout       io.Writer
	Stderr       io.Writer
}

// NewCommand returns the cobra command for `gplay auth status`.
func NewCommand(opts Options) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account and where its credential lives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(opts, output)
		},
	}
	cmd.Flags().StringVar(&output, "output", "table", "output format: table or json")

	if opts.Stdout != nil {
		cmd.SetOut(opts.Stdout)
	}
	if opts.Stderr != nil {
		cmd.SetErr(opts.Stderr)
	}
	return cmd
}

type payload struct {
	Name        string `json:"name"`
	ClientEmail string `json:"client_email"`
	Path        string `json:"path"`
}

func run(opts Options, output string) error {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	be := keystore.NewFileBackend(opts.KeystoreRoot)

	sa, err := resolver.New(cfg, be).Resolve()
	if err != nil {
		return err
	}
	active, _ := cfg.Active()
	p := payload{
		Name:        active.Name,
		ClientEmail: sa.ClientEmail,
		Path:        filepath.Join(opts.KeystoreRoot, active.Name+".json"),
	}

	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	switch output {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case "table", "":
		_, err := fmt.Fprintf(stdout, "Active account: %s\nClient email:   %s\nPath:           %s\n",
			p.Name, p.ClientEmail, p.Path)
		return err
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}
