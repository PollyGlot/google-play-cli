// Package viewcmd implements `gplay apps view`: a read-only sanity
// check on what the active service account sees for a given app.
// Opens a Google Play Edit, reads details.get + listings.get on the
// app's default language, discards the Edit, and renders the trio
// {defaultLanguage, title, contactEmail} that confirms "yes, I'm
// looking at the right app".
//
// Like `gplay releases list`, it is thin glue over internal/play —
// resolve --package, build an authenticated client, call
// internal/play/details.Get, and render. The Edit is opened and
// discarded inside details.Get via edits.WithReadOnlyEdit; nothing is
// ever committed.
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

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
}

// usageError is a CLI-misuse error (missing --package and no pin);
// ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// validationError is a client-side package-name format failure with
// ExitCode()=20 per docs/DESIGN.md §9 (client-side validation). Same
// gate as addcmd: the cheapest shape check (non-empty, reverse-DNS dot)
// before any HTTP round-trip. Catches typos at the CLI boundary so the
// user sees a clear "not a valid Android package name" rather than a
// generic 404 from edits.insert.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) ExitCode() int { return 20 }

// validatePackage applies the same cheap client-side check as
// addcmd.validatePackage: non-empty + at least one dot (Google Play
// uses reverse-DNS package names, so a missing dot is a typo). Kept
// local rather than imported from addcmd to keep its message wording
// scoped to "apps view" — the two commands surface their errors with
// their own command prefix, which is the project's convention.
func validatePackage(pkg string) error {
	if !strings.Contains(pkg, ".") {
		return &validationError{msg: fmt.Sprintf("apps view: %q is not a valid Android package name (must contain a dot, e.g. com.example.myapp)", pkg)}
	}
	return nil
}

// Payload satisfies output.Renderable. Raw carries the
// {"details":..,"listing":..,"icon"?:..} envelope for the --output json
// pass-through (explicit exception to ADR-0003); the typed fields drive
// the table and markdown renderers. Package is carried for context in
// the human-facing views. IconSha256 is the [experimental] icon content
// hash, empty when the default language's icon slot is empty — the
// table/markdown views show a sha256 line only when it is non-empty.
type Payload struct {
	Package         string          `json:"-"`
	DefaultLanguage string          `json:"defaultLanguage"`
	Title           string          `json:"title"`
	ContactEmail    string          `json:"contactEmail"`
	IconSha256      string          `json:"-"`
	Raw             json.RawMessage `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// JSON emits the raw envelope verbatim; table and markdown are
// human-shaped views over the three Details fields.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes a `Field  Value` two-column table. The package
// header sits above so a reader of `gplay apps view` knows immediately
// which app they are looking at.
func renderTable(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "PACKAGE: %s\n", p.Package); err != nil {
		return err
	}
	rows := [][2]string{
		{"DEFAULT_LANGUAGE", p.DefaultLanguage},
		{"TITLE", p.Title},
		{"CONTACT_EMAIL", p.ContactEmail},
	}
	// [experimental] icon: only when the default language's icon slot is
	// non-empty. sha256 is the durable content-identity handle (ADR-0038);
	// the preview url is never surfaced in a human view.
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

// renderJSON emits the raw {"details":..,"listing":..} envelope
// verbatim. Falls back to the typed Payload only if the envelope was
// somehow not captured (defensive — Run always populates Raw on
// success).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, p)
}

// renderMarkdown emits a `- **Field**: value` list per docs/DESIGN.md
// §7 — single-record info commands render as a list, not as a GFM
// table. The package name leads as a level-2 heading so the rendered
// markdown drops cleanly into a PR comment or a docs page.
func renderMarkdown(w io.Writer, p Payload) error {
	if _, err := fmt.Fprintf(w, "## %s\n\n- **Default language**: %s\n- **Title**: %s\n- **Contact email**: %s\n",
		p.Package, p.DefaultLanguage, p.Title, p.ContactEmail); err != nil {
		return err
	}
	// [experimental] icon sha256 line, only when present (ADR-0038).
	if p.IconSha256 != "" {
		if _, err := fmt.Fprintf(w, "- **Icon sha256**: %s\n", p.IconSha256); err != nil {
			return err
		}
	}
	return nil
}

// Run is the business function the kernel invokes. It resolves the
// package (--package flag → repo pin → usage error), builds an
// authenticated HTTP client, and calls details.Get — which itself
// opens and discards the Edit, so there is no mutation path here.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
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

// NewCommand returns the cobra command for `gplay apps view`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show default language, title, and contact email for an app",
		Long: `Show the app's defaultLanguage, title (on the default language),
and contactEmail — the minimum needed to confirm "yes, I'm looking at the
right app".

Reads from the Google Play Developer API inside a read-only Edit (open →
details.get + listings.get + images.list(icon) → discard); nothing is
committed. The package defaults to the repo's .gplay/config.json pin when
--package is omitted.

[experimental] When the default language has a store icon, the view also
reports the icon's content sha256 (table/markdown) and adds an "icon"
key {"url":..,"sha256":..} to the JSON envelope. The sha256 is the
durable content-identity handle; the url is an ephemeral preview link —
never persist it (fetch the bytes with 'gplay metadata images pull').
gplay does not cache the icon: each run is a faithful live read.

--output json returns the gplay envelope
{"details":..,"listing":..,"icon"?:..} — each sub-object is the upstream
API body verbatim. (Explicit exception to ADR-0003: multiple endpoints
are merged here, so the JSON shape is gplay-defined rather than a single
API pass-through. The icon key is omitted when the icon slot is empty.)`,
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
