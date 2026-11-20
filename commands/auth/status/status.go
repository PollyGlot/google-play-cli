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

type Input struct{}

type Payload struct {
	Active      bool   `json:"active"`
	Name        string `json:"name,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Path        string `json:"path,omitempty"`
}

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

func Run(rc *kernel.RunContext, _ Input) (output.Renderable, error) {
	if err := rc.EnsureAccount(); err != nil {
		return nil, err
	}
	if rc.Account == nil {
		return Payload{Active: false}, nil
	}
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
	} else if rc.KeystoreLabel == keystore.BackendKeyring {
		keystore.WarnStrayFiles(rc.Ctx, rc.Stderr, rc.KeystoreRoot)
	}
	return p, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print the active Account, the keystore backend, and where the credential lives",
		Example: `  gplay auth status
  gplay auth status --output json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
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
