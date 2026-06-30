package detailscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// SetInput is the request-shaped struct cobra builds for the write. Each
// field carries its value AND a `…Set` boolean recording whether the flag
// was passed at all (cmd.Flags().Changed). That distinction is the whole
// point of a partial patch: "--contact-phone not passed" (leave intact)
// must be told apart from "--contact-phone "" passed" (clear it).
type SetInput struct {
	Package string

	DefaultLanguage    string
	DefaultLanguageSet bool
	ContactEmail       string
	ContactEmailSet    bool
	ContactPhone       string
	ContactPhoneSet    bool
	ContactWebsite     string
	ContactWebsiteSet  bool

	DryRun            bool
	KeepEditOnFailure bool
}

// anyFieldSet reports whether at least one field flag was passed. A bare
// `set` with none is the no-flag footgun — refused before any work.
func anyFieldSet(in SetInput) bool {
	return in.DefaultLanguageSet || in.ContactEmailSet || in.ContactPhoneSet || in.ContactWebsiteSet
}

// buildPatch turns the flags into an AppDetailsPatch, posing ONLY the
// fields the user passed (Changed). A pure function: given the same
// SetInput it always yields the same patch, so it is exercised through
// the e2e tests' observed patch bodies rather than a dedicated unit test
// (the PRD's "more e2e than function tests" preference).
func buildPatch(in SetInput) details.AppDetailsPatch {
	var p details.AppDetailsPatch
	if in.DefaultLanguageSet {
		v := in.DefaultLanguage
		p.DefaultLanguage = &v
	}
	if in.ContactEmailSet {
		v := in.ContactEmail
		p.ContactEmail = &v
	}
	if in.ContactPhoneSet {
		v := in.ContactPhone
		p.ContactPhone = &v
	}
	if in.ContactWebsiteSet {
		v := in.ContactWebsite
		p.ContactWebsite = &v
	}
	return p
}

// SetPayload satisfies output.Renderable for the write. Raw carries the
// details.patch body for the ADR-0003 JSON pass-through (empty on
// --dry-run, which never hits the API); Patch drives the human-shaped
// preview of which fields were (or would be) written; DryRun tags the
// preview. Package is context for the human views, never in the JSON
// (which stays a faithful details.patch pass-through).
type SetPayload struct {
	Package string                  `json:"-"`
	Patch   details.AppDetailsPatch `json:"-"`
	Raw     json.RawMessage         `json:"-"`
	DryRun  bool                    `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format. The
// JSON form is the ADR-0003 details.patch pass-through; table and
// markdown are human-shaped views over the fields the patch touched.
func (p SetPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderSetTable(w, p) },
		JSON:     func(w io.Writer) error { return renderSetJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderSetMarkdown(w, p) },
	}
}

// patchRows lists the fields the patch touches, in a stable order, paired
// with the value written. An explicit empty value renders as `(cleared)`
// so a deliberate clear is never mistaken for a blank cell.
func patchRows(p details.AppDetailsPatch) [][2]string {
	var rows [][2]string
	add := func(label string, v *string) {
		if v == nil {
			return
		}
		val := *v
		if val == "" {
			val = "(cleared)"
		}
		rows = append(rows, [2]string{label, val})
	}
	add("DEFAULT_LANGUAGE", p.DefaultLanguage)
	add("CONTACT_EMAIL", p.ContactEmail)
	add("CONTACT_PHONE", p.ContactPhone)
	add("CONTACT_WEBSITE", p.ContactWebsite)
	return rows
}

// dryRunSuffix returns the ` (dry-run)` marker appended to the package
// header when nothing was written, so a previewed patch is never mistaken
// for an applied one.
func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

// renderSetTable writes a package-context header (with a dry-run marker
// when previewing) then a `FIELD  VALUE` table of the touched fields.
func renderSetTable(w io.Writer, p SetPayload) error {
	if _, err := fmt.Fprintf(w, "PACKAGE: %s%s\n", p.Package, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "FIELD\tVALUE"); err != nil {
		return err
	}
	for _, r := range patchRows(p.Patch) {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", r[0], r[1]); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// renderSetJSON emits the raw details.patch body verbatim (ADR-0003
// pass-through) for an applied write. A --dry-run makes no API call, so
// there is no body to pass through — but --output json is the DEFAULT in
// pipes/CI (docs/DESIGN.md), exactly where a dry-run is most likely to
// run, so it must still produce parseable output rather than erroring.
// We emit a gplay-shaped preview object, clearly tagged `dryRun` and
// carrying only the fields the patch would touch; it is unmistakable from
// a real details.patch response (which echoes the full resource and has
// no `dryRun` key). An empty Raw with no dry-run is a contract break we
// surface loudly rather than emitting zero bytes.
func renderSetJSON(w io.Writer, p SetPayload) error {
	if len(p.Raw) == 0 {
		if p.DryRun {
			return output.WriteJSON(w, struct {
				DryRun bool                    `json:"dryRun"`
				Patch  details.AppDetailsPatch `json:"patch"`
			}{DryRun: true, Patch: p.Patch})
		}
		return fmt.Errorf("missing raw details.patch payload for --output json")
	}
	_, err := w.Write(p.Raw)
	return err
}

// renderSetMarkdown writes the touched fields as a GFM table preceded by
// the same package-context line (with the dry-run marker) as the table
// view, so a pasted report stands on its own.
func renderSetMarkdown(w io.Writer, p SetPayload) error {
	if _, err := fmt.Fprintf(w, "## %s%s\n\n", p.Package, dryRunSuffix(p.DryRun)); err != nil {
		return err
	}
	rows := patchRows(p.Patch)
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r[0], r[1]})
	}
	return output.MarkdownTable(w, []string{"FIELD", "VALUE"}, out)
}

// RunSet is the business function the kernel invokes for the write. It
// enforces the no-flag guard, resolves the package, and — unless
// --dry-run short-circuits before any network — opens an implicit Edit,
// PATCHes the App details field-by-field, and commits. There is no
// --confirm: contact info is low-stakes and reversible (same reasoning as
// `testers set`). There is no offline format validation: the API arbitrates
// email/URL/phone shape, and its error surfaces verbatim.
func RunSet(rc *kernel.RunContext, in SetInput) (output.Renderable, error) {
	// No-flag guard: a bare `set` with no field flag must never reach the
	// API — it would emit an empty patch. This is the footgun stop, not
	// validation. Run it first, before package resolution, so the misuse
	// is reported regardless of pin state.
	if !anyFieldSet(in) {
		return nil, &usageError{msg: "refusing to patch App details without any field flag; pass at least one of --default-language, --contact-email, --contact-phone, --contact-website (an empty value clears a field, e.g. --contact-phone \"\")"}
	}

	pkg := strings.TrimSpace(in.Package)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	patch := buildPatch(in)

	// Dry-run skips auth entirely: nothing hits the network, so a missing
	// Account is not a problem here. We preview the exact patch that would
	// be written (cleared fields included) without opening an Edit.
	if in.DryRun {
		return SetPayload{Package: pkg, Patch: patch, DryRun: true}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-set Edit.
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID}, func(editID string) error {
		_, r, e := details.Patch(rc.Ctx, httpClient, pkg, editID, patch)
		if e != nil {
			return e
		}
		raw = r
		return nil
	}); err != nil {
		return nil, classifyEditError(pkg, err)
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr. The
	// --dry-run path returned above, so this only runs after a real patch.
	rc.ConfirmMutation(explicitEditID, "app details updated for %q", pkg)
	return SetPayload{Package: pkg, Patch: patch, Raw: raw}, nil
}

// NewSetCommand returns the cobra command for `gplay apps details set`.
func NewSetCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         SetInput
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set an app's App details fields (default language, contact email/phone/website)",
		Long: `Set the App details fields one by one. set is a partial patch at flag
granularity: a field you pass is written, a field you omit is left
intact, and an explicit empty value clears a field (e.g.
--contact-phone "" removes the number). It maps to edits.details.patch.

There is no --confirm: contact info is low-stakes and reversible (same as
testers set). There is no offline format validation: the API arbitrates
email / URL / phone shape, and its error surfaces verbatim.

A bare ` + "`set`" + ` with no field flag is refused (exit 2) so a forgotten flag
can never emit an empty patch. Writes inside an implicit Edit (open →
details.patch → commit). Use --dry-run to preview the patch without any
HTTP call (no auth needed), and --keep-edit-on-failure to skip the
auto-discard cleanup for debugging.

--output json returns the edits.details.patch body verbatim.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.DefaultLanguageSet = cmd.Flags().Changed("default-language")
			in.ContactEmailSet = cmd.Flags().Changed("contact-email")
			in.ContactPhoneSet = cmd.Flags().Changed("contact-phone")
			in.ContactWebsiteSet = cmd.Flags().Changed("contact-website")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return RunSet(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.DefaultLanguage, "default-language", "", "set the app's default language (BCP-47 locale, e.g. en-US)")
	cmd.Flags().StringVar(&in.ContactEmail, "contact-email", "", "set the user-visible contact email (empty value clears it)")
	cmd.Flags().StringVar(&in.ContactPhone, "contact-phone", "", "set the user-visible contact phone (empty value clears it)")
	cmd.Flags().StringVar(&in.ContactWebsite, "contact-website", "", "set the user-visible contact website (empty value clears it)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "preview the patch without any HTTP call (no auth needed)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	return cmd
}
