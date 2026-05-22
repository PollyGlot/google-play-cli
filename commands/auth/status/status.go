// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, the keystore backend in use, and
// (when applicable) the on-disk credential path. The output Format is
// resolved by internal/output: TTY → table, pipe/CI → json, and
// --output markdown returns an idiomatic definition list.
//
// When no credential resolves (no active Account, no env override),
// status prints a friendly "no active account" payload and exits 0 — it
// is informational, not a hard error.
package status

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/output"
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
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account, the keystore backend, and where the credential lives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Verbose is a root-level persistent flag (docs/DESIGN.md §8). We
			// read it via the inherited flag set so `-v` works at any
			// position on the command line.
			verbose, _ := cmd.Flags().GetBool("verbose")
			return run(cmd, opts, output.Format(outputFlag), verbose)
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}

type payload struct {
	Active      bool   `json:"active"`
	Name        string `json:"name,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Path        string `json:"path,omitempty"`
}

func run(cmd *cobra.Command, opts Options, format output.Format, verbose bool) error {
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
			return output.Render(cmd.OutOrStdout(), format, emptyRenderers())
		}
		return err
	}
	// ConfigAccount is empty when --service-account or GPLAY_SERVICE_ACCOUNT
	// wins resolution: the credential bytes came from outside the config so
	// no Account name applies. Surface that explicitly and skip the file
	// path (which is keyed by Account name, so meaningless here).
	activeName := resolved.ConfigAccount
	p := payload{
		Active:      true,
		Name:        activeName,
		ClientEmail: sa.ClientEmail,
		Backend:     label,
	}
	if activeName == "" {
		p.Name = "(env override)"
	} else if label == keystore.BackendFile {
		p.Path = filepath.Join(opts.KeystoreRoot, activeName+".json")
	}

	return output.Render(cmd.OutOrStdout(), format, renderersFor(p))
}

// renderersFor wires the three Format renderers for a populated payload.
func renderersFor(p payload) output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// emptyRenderers wires the three renderers for the "no active account"
// payload. Exit code stays 0 — this is state, not failure.
func emptyRenderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			if _, err := fmt.Fprintln(w, "No active account."); err != nil {
				return err
			}
			_, err := fmt.Fprintln(w, "Run `gplay auth login` to register one, or `gplay auth list` to see registered Accounts.")
			return err
		},
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, payload{Active: false}) },
		Markdown: renderEmptyMarkdown,
	}
}

func renderTable(w io.Writer, p payload) error {
	if _, err := fmt.Fprintf(w, "Active account: %s\nClient email:   %s\n", p.Name, p.ClientEmail); err != nil {
		return err
	}
	if p.Path == "" {
		_, err := fmt.Fprintf(w, "Backend:        %s\n", p.Backend)
		return err
	}
	_, err := fmt.Fprintf(w, "Backend:        %s: %s\n", p.Backend, p.Path)
	return err
}

// renderMarkdown emits a "- **Field**: value" definition list. Path is
// omitted when the backend is keyring (where it would be meaningless).
func renderMarkdown(w io.Writer, p payload) error {
	if _, err := fmt.Fprintf(w, "- **Active account**: %s\n", p.Name); err != nil {
		return err
	}
	if p.ClientEmail != "" {
		if _, err := fmt.Fprintf(w, "- **Client email**: %s\n", p.ClientEmail); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "- **Backend**: %s\n", p.Backend); err != nil {
		return err
	}
	if p.Path != "" {
		if _, err := fmt.Fprintf(w, "- **Path**: %s\n", p.Path); err != nil {
			return err
		}
	}
	return nil
}

func renderEmptyMarkdown(w io.Writer) error {
	_, err := fmt.Fprintln(w, "**No active account.**\n\nRun `gplay auth login` to register one, or `gplay auth list` to see registered Accounts.")
	return err
}
