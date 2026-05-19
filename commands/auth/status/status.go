// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, and where the credential is stored.
// JSON output is supported via `--output json`.
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
}

// NewCommand returns the cobra command for `gplay auth status`.
func NewCommand(opts Options) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account and where its credential lives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, output)
		},
	}
	cmd.Flags().StringVar(&output, "output", "table", "output format: table or json")
	return cmd
}

type payload struct {
	Name        string `json:"name"`
	ClientEmail string `json:"client_email"`
	Path        string `json:"path"`
}

func run(cmd *cobra.Command, opts Options, output string) error {
	cfg, err := config.LoadOrEmpty(opts.ConfigPath)
	if err != nil {
		return err
	}
	be := keystore.NewFileBackend(opts.KeystoreRoot)

	sa, err := resolver.New(cfg, be).Resolve()
	if err != nil {
		return err
	}
	// Active() can only be ok here: Resolve already short-circuited otherwise.
	active, _ := cfg.Active()
	p := payload{
		Name:        active.Name,
		ClientEmail: sa.ClientEmail,
		Path:        filepath.Join(opts.KeystoreRoot, active.Name+".json"),
	}

	stdout := cmd.OutOrStdout()
	switch output {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case "table":
		_, err := fmt.Fprintf(stdout, "Active account: %s\nClient email:   %s\nPath:           %s\n",
			p.Name, p.ClientEmail, p.Path)
		return err
	default:
		return fmt.Errorf("unsupported --output %q (want table or json)", output)
	}
}
