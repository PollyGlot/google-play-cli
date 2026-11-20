package detailscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

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
	Commit            commitflags.Flags
}

func anyFieldSet(in SetInput) bool {
	return in.DefaultLanguageSet || in.ContactEmailSet || in.ContactPhoneSet || in.ContactWebsiteSet
}

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

type SetPayload struct {
	Package string                  `json:"-"`
	Patch   details.AppDetailsPatch `json:"-"`
	Raw     json.RawMessage         `json:"-"`
	DryRun  bool                    `json:"-"`
}

func (p SetPayload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderSetTable(w, p) },
		JSON:     func(w io.Writer) error { return renderSetJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderSetMarkdown(w, p) },
	}
}

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

func dryRunSuffix(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

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

func RunSet(rc *kernel.RunContext, in SetInput) (output.Renderable, error) {
	if !anyFieldSet(in) {
		return nil, &exit.UsageError{Msg: "refusing to patch App details without any field flag; pass at least one of --default-language, --contact-email, --contact-phone, --contact-website (an empty value clears a field, e.g. --contact-phone \"\")"}
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	patch := buildPatch(in)

	if in.DryRun {
		return SetPayload{Package: pkg, Patch: patch, DryRun: true}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID, Commit: in.Commit.For(rc, explicitEditID)}, func(editID string) error {
		_, r, e := details.Patch(rc.Ctx, httpClient, pkg, editID, patch)
		if e != nil {
			return e
		}
		raw = r
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	rc.ConfirmMutation(explicitEditID, "app details updated for %q", pkg)
	return SetPayload{Package: pkg, Patch: patch, Raw: raw}, nil
}

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
		Example: `  # Update the support contacts shown on the store
  gplay apps details set --contact-email support@example.com --contact-website https://example.com

  # Preview clearing the phone number, without any HTTP call
  gplay apps details set --contact-phone "" --dry-run`,
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
	commitflags.Register(cmd, &in.Commit)
	return cmd
}
