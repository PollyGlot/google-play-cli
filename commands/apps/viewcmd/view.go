package viewcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
)

type Input struct {
	Package string
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

func validatePackage(pkg string) error {
	if !strings.Contains(pkg, ".") {
		return &validationError{msg: fmt.Sprintf("apps view: %q is not a valid Android package name (must contain a dot, e.g. com.example.myapp)", pkg)}
	}
	return nil
}

type Payload struct {
	Package         string          `json:"-"`
	DefaultLanguage string          `json:"defaultLanguage"`
	Title           string          `json:"title"`
	ContactEmail    string          `json:"contactEmail"`
	IconSha256      string          `json:"-"`
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
		{"TITLE", p.Title},
		{"CONTACT_EMAIL", p.ContactEmail},
	}
	if p.IconSha256 != "" {
		rows = append(rows, [2]string{"ICON_SHA256", p.IconSha256})
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
	if _, err := fmt.Fprintf(w, "## %s\n\n- **Default language**: %s\n- **Title**: %s\n- **Contact email**: %s\n",
		p.Package, p.DefaultLanguage, p.Title, p.ContactEmail); err != nil {
		return err
	}
	if p.IconSha256 != "" {
		if _, err := fmt.Fprintf(w, "- **Icon sha256**: %s\n", p.IconSha256); err != nil {
			return err
		}
	}
	return nil
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	if err := validatePackage(pkg); err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	d, raw, err := details.Get(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, err
	}
	payload := Payload{
		Package:         pkg,
		DefaultLanguage: d.DefaultLanguage,
		Title:           d.Title,
		ContactEmail:    d.ContactEmail,
		Raw:             raw,
	}
	if d.Icon != nil {
		payload.IconSha256 = d.Icon.Sha256
	}
	return payload, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show default language, title, and contact email for an app",
		Long: `Show the app's defaultLanguage, title (on the default language),
and contactEmail: the minimum needed to confirm "yes, I'm looking at the
right app".

Reads from the Google Play Developer API inside a read-only Edit (open →
details.get + listings.get + images.list(icon) → discard); nothing is
committed. The package defaults to the repo's .gplay/config.json pin when
--package is omitted.

[experimental] When the default language has a store icon, the view also
reports the icon's content sha256 (table/markdown) and adds an "icon"
key {"url":..,"sha256":..} to the JSON envelope. The sha256 is the
durable content-identity handle; the url is an ephemeral preview link:
never persist it (fetch the bytes with 'gplay metadata images pull').
gplay does not cache the icon: each run is a faithful live read.

--output json returns the gplay envelope
{"details":..,"listing":..,"icon"?:..}: each sub-object is the upstream
API body verbatim. (An exception to the verbatim rule: multiple endpoints
are merged here, so the JSON shape is gplay-defined rather than a single
API pass-through. The icon key is omitted when the icon slot is empty.)`,
		Example: `  # Confirm you are pointed at the right app
  gplay apps view

  gplay apps view --package com.example.app --output json`,
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
