// Package list implements `gplay releases system-apks list`: enumerate the
// System APK variants created from the bundle uploaded under a versionCode.
// Read-only and Edit-free (a direct application-scoped GET). Ships
// [experimental].
package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/systemapkscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	VersionCode int64
	Columns     string
}

// Payload renders the variants as a table / verbatim JSON.
type Payload struct {
	Rows []systemapkscmd.Row
	Cols []output.Column[systemapkscmd.Row]
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Cols, p.Rows) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, p.Rows) },
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
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
	lr, raw, err := systemapks.List(rc.Ctx, httpClient, pkg, in.VersionCode)
	if err != nil {
		return nil, systemapkscmd.Classify(pkg, err)
	}
	return Payload{Rows: systemapkscmd.BuildRows(lr.Variants), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases system-apks list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the system APK variants created from an uploaded AAB",
		Long: `List the system APK variants created from the App Bundle uploaded under a
given versionCode, each with the device spec and options it was built for. A
variant's variantId feeds ` + "`gplay releases system-apks view`" + ` and
` + "`gplay releases system-apks download`" + `.

--version-code is required (the resource is keyed by version). --output json
passes the SystemApksListResponse through verbatim.

This is a direct application-scoped read: it opens no Edit.`,
		Example: `  gplay releases system-apks list --version-code 1042
  gplay releases system-apks list --version-code 1042 --columns variantId,abis
  gplay releases system-apks list --version-code 1042 --output json`,
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
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bundle versionCode whose system APK variants to list (required)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+systemapkscmd.DefaultColumns()+")")
	return cmd
}
