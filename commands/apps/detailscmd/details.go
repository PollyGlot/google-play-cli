package detailscmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

type Input struct {
	Package string
}

type Payload struct {
	Package         string          `json:"-"`
	DefaultLanguage string          `json:"defaultLanguage"`
	ContactEmail    string          `json:"contactEmail"`
	ContactPhone    string          `json:"contactPhone"`
	ContactWebsite  string          `json:"contactWebsite"`
	Raw             json.RawMessage `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "PACKAGE: %s\n", p.Package); err != nil {
		return err
	}
	rows := [][2]string{
		{"DEFAULT_LANGUAGE", p.DefaultLanguage},
		{"CONTACT_EMAIL", p.ContactEmail},
		{"CONTACT_PHONE", p.ContactPhone},
		{"CONTACT_WEBSITE", p.ContactWebsite},
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", r[0], r[1]); err != nil {
			return err
		}
	}
	return nil
}

func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, p)
}

func renderMarkdown(w io.Writer, p Payload) error {
	_, err := fmt.Fprintf(w,
		"## %s\n\n- **Default language**: %s\n- **Contact email**: %s\n- **Contact phone**: %s\n- **Contact website**: %s\n",
		p.Package, p.DefaultLanguage, p.ContactEmail, p.ContactPhone, p.ContactWebsite)
	return err
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	d, raw, err := details.GetDetails(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}
	return Payload{
		Package:         pkg,
		DefaultLanguage: d.DefaultLanguage,
		ContactEmail:    d.ContactEmail,
		ContactPhone:    d.ContactPhone,
		ContactWebsite:  d.ContactWebsite,
		Raw:             raw,
	}, nil
}

func NewViewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show an app's App details (default language, contact email/phone/website)",
		Long: `Show the full edits.details record for an app: defaultLanguage and the
user-visible contactEmail, contactPhone, and contactWebsite.

Reads from the Google Play Developer API inside a read-only Edit (open →
details.get → discard); nothing is committed. The package defaults to the
repo's .gplay/config.json pin when --package is omitted.

--output json returns the edits.details.get body verbatim (a clean
pass-through: a single endpoint is read, so there is no gplay
envelope). This is distinct from ` + "`gplay apps view`" + `, the terse
cross-resource identity card (package + title + default language).`,
		Example: `  gplay apps details view
  gplay apps details view --package com.example.app --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	return cmd
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "details",
		Short: "Read and set an app's App details (default language, contact email/phone/website)",
		Long: `Group for an app's App details: the app-global edits.details record
holding defaultLanguage and the user-visible contactEmail, contactPhone,
and contactWebsite.

` + "`apps details view`" + ` reads the record; ` + "`apps details set`" + ` writes it
field-by-field. The bare ` + "`apps details`" + ` command prints this help.`,
		RunE:          kernel.GroupRunE,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(NewViewCommand(boot))
	cmd.AddCommand(kernel.MarkMutating(NewSetCommand(boot)))
	return cmd
}
