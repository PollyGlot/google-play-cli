// Package view implements `gplay releases system-apks view <variantId>`: read
// one System APK variant. Read-only and Edit-free. Ships [experimental].
package view

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/systemapkscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
)

// Input is the request-shaped struct cobra builds from the positional + flags.
type Input struct {
	Package     string
	VersionCode int64
	VariantID   string
	Columns     string
}

// Payload renders one variant as a single-row table / verbatim JSON.
type Payload struct {
	Row  systemapkscmd.Row
	Cols []output.Column[systemapkscmd.Row]
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Cols, []systemapkscmd.Row{p.Row}) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []systemapkscmd.Row{p.Row}) },
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := systemapkscmd.ParseVariantID(in.VariantID)
	if err != nil {
		return nil, err
	}
	if err := systemapkscmd.RequireVersionCode(in.VersionCode); err != nil {
		return nil, err
	}
	cols, err := systemapkscmd.ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	v, raw, err := systemapks.Get(rc.Ctx, httpClient, pkg, in.VersionCode, id)
	if err != nil {
		return nil, systemapkscmd.Classify(pkg, err)
	}
	return Payload{Row: systemapkscmd.BuildRow(v), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases system-apks view`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <variantId>",
		Short: "Read one system APK variant",
		Long: `Read one system APK variant by the <variantId> from
` + "`gplay releases system-apks list`" + ` and the --version-code of the bundle it
was created from. --output json passes the Variant through verbatim.

This is a direct application-scoped read: it opens no Edit.`,
		Example: `  gplay releases system-apks view 3 --version-code 1042
  gplay releases system-apks view 3 --version-code 1042 --output json`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.VariantID = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bundle versionCode the variant was created from (required)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+systemapkscmd.DefaultColumns()+")")
	return cmd
}
