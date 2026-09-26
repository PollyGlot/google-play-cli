// Package detailscmd implements `apps details`, a pure grouping noun over
// the App details resource: the app-global edits.details record
// holding defaultLanguage and the user-visible contact email/phone/website.
//
// Per ADR-0019 a read always carries a verb, so the group holds two
// subcommands and the bare `details` only prints help:
//   - `apps details view` reads the record (open a read-only Edit →
//     details.get → discard; nothing is committed),
//   - `apps details set` writes it field-by-field.
//
// The read is thin glue over internal/play/details: resolve --package,
// build an authenticated client, call details.GetDetails, which opens and
// discards a read-only Edit internally, and render. Because GetDetails
// reads a SINGLE endpoint, --output json is the details.get body verbatim:
// a clean ADR-0003 pass-through with no gplay envelope (unlike `apps view`,
// which merges details+listing and carries a documented exception).
// `apps view` stays the terse cross-resource identity card;
// `apps details view` is the full edits.details record.
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

// Input is the request-shaped struct cobra builds from flags for the read.
type Input struct {
	Package string
}

// Payload satisfies output.Renderable. Raw carries the details.get body
// for the ADR-0003 JSON pass-through; the four typed fields drive the
// table and markdown renderers. Package is carried for context in the
// human-facing views (never in the JSON, which stays a faithful
// details.get pass-through).
type Payload struct {
	Package         string          `json:"-"`
	DefaultLanguage string          `json:"defaultLanguage"`
	ContactEmail    string          `json:"contactEmail"`
	ContactPhone    string          `json:"contactPhone"`
	ContactWebsite  string          `json:"contactWebsite"`
	Raw             json.RawMessage `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// JSON emits the details.get body verbatim; table and markdown are
// human-shaped views over the four App details fields.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes a `Field  Value` two-column table under a package
// header so a reader knows immediately which app's App details they are
// looking at.
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

// renderJSON emits the raw details.get body verbatim (ADR-0003
// pass-through). Falls back to the typed Payload only if the body was
// somehow not captured (defensive: Run always populates Raw on success).
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, p)
}

// renderMarkdown emits a `- **Field**: value` list per docs/DESIGN.md §7
// : a single-record read renders as a list, not as a GFM table. The
// package name leads as a level-2 heading so the rendered markdown drops
// cleanly into a PR comment or a docs page.
func renderMarkdown(w io.Writer, p Payload) error {
	_, err := fmt.Fprintf(w,
		"## %s\n\n- **Default language**: %s\n- **Contact email**: %s\n- **Contact phone**: %s\n- **Contact website**: %s\n",
		p.Package, p.DefaultLanguage, p.ContactEmail, p.ContactPhone, p.ContactWebsite)
	return err
}

// Run is the business function the kernel invokes for the read. It
// resolves the package (--package flag → repo pin → usage error), builds
// an authenticated HTTP client, and calls details.GetDetails, which
// itself opens and discards a read-only Edit, so there is no mutation
// path here.
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

// NewViewCommand returns the cobra command for `gplay apps details view`:
// the read of the full edits.details record. Per ADR-0019 the read carries
// an explicit verb: the bare `details` group prints help, it never reads.
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
ADR-0003 pass-through: a single endpoint is read, so there is no gplay
envelope). This is distinct from ` + "`gplay apps view`" + `, the terse
cross-resource identity card (package + title + default language).`,
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

// NewCommand returns the cobra group for `apps details`: a pure grouping
// noun (ADR-0019). It holds `view` (read) and `set` (write); there is no
// verb-less read. kernel.GroupRunE makes the bare command print help while a
// mistyped or removed subcommand fails loudly as CLI misuse (exit 2) rather
// than silently printing help with exit 0.
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
	// `apps details set` writes the edits.details record, so it is a mutating
	// command refused under GPLAY_READONLY (kernel.MarkMutating / ADR-0024);
	// `apps details view` is a read and stays unmarked.
	cmd.AddCommand(kernel.MarkMutating(NewSetCommand(boot)))
	return cmd
}
