// Package status implements `gplay auth status`: print which Account is
// active, the underlying client_email, the keystore backend in use, and
// (when applicable) the on-disk credential path.
package status

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/auth/keystore"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Input has no command-specific flags.
type Input struct{}

// Payload is the JSON-shaped status snapshot.
type Payload struct {
	Active      bool   `json:"active"`
	Name        string `json:"name,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Path        string `json:"path,omitempty"`
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	if !p.Active {
		return output.Renderers{
			Table: func(w io.Writer) error {
				if _, err := fmt.Fprintln(w, "No active account."); err != nil {
					return err
				}
				_, err := fmt.Fprintln(w, "Run `gplay auth login` to register one, or `gplay auth list` to see registered Accounts.")
				return err
			},
			JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
			Markdown: renderEmptyMarkdown,
		}
	}
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// Run reads the resolved cascade + Account from rc and shapes the payload.
// status keeps RequireAccount=false; rc.Account == nil simply maps to
// the "no active account" payload.
func Run(rc *kernel.RunContext, _ Input) (output.Renderable, error) {
	// status reports auth state, so resolving the credential (and probing
	// the keystore) is exactly its job — do it now rather than at boot.
	// EnsureAccount splits the two failure shapes (ADR-0020): an *invalid*
	// credential (malformed JSON, missing field, unreadable file) returns an
	// exit-10 error, which we surface before rendering so a corrupt active
	// credential hard-errors rather than masquerading as "no account"; an
	// *absent* credential returns nil with rc.Account == nil, which maps to
	// the benign "no active account" payload.
	if err := rc.EnsureAccount(); err != nil {
		return nil, err
	}
	if rc.Account == nil {
		return Payload{Active: false}, nil
	}
	// rc.AccountName is the credential actually in use — it honours
	// --account / GPLAY_ACCOUNT overrides, unlike rc.Resolved.ConfigAccount,
	// which only reflects the cascade. It is empty only for an inline
	// credential (--service-account / GPLAY_SERVICE_ACCOUNT) that never came
	// from the keystore, so the backend label is probed only when there IS a
	// stored Account — keeping the inline/env-override path keyring-free. For
	// a stored Account EnsureAccount already selected the backend while
	// loading the credential, so this Backend() call is a memoised no-op.
	activeName := rc.AccountName
	if activeName != "" {
		if _, err := rc.Backend(); err != nil {
			return nil, err
		}
	}
	p := Payload{
		Active:      true,
		Name:        activeName,
		ClientEmail: rc.Account.ClientEmail,
		Backend:     rc.KeystoreLabel,
	}
	if activeName == "" {
		p.Name = "(env override)"
	} else if rc.KeystoreLabel == keystore.BackendFile {
		p.Path = filepath.Join(rc.KeystoreRoot, activeName+".json")
	}
	return p, nil
}

// NewCommand returns the cobra command for `gplay auth status`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account, the keystore backend, and where the credential lives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, Input{})
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	return cmd
}

func renderTable(w io.Writer, p Payload) error {
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

func renderMarkdown(w io.Writer, p Payload) error {
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
