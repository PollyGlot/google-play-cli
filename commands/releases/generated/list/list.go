// Package list implements `gplay releases generated list`: enumerate the APKs
// Play generated and signed from an uploaded AAB for a given versionCode.
// Read-only and Edit-free: a direct application-scoped GET (the generatedApks
// endpoints are not under /edits/). The API keys the resource on versionCode, so
// gplay requires --version-code; missing it is CLI misuse (exit 2) caught before
// any network. Ships [experimental].
package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/generated/generatedcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	VersionCode int64
	Columns     string
}

// Payload renders the generated APKs as a table / verbatim JSON.
type Payload struct {
	Rows []generatedcmd.Row
	Cols []output.Column[generatedcmd.Row]
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
	if in.VersionCode <= 0 {
		return nil, generatedcmd.Usagef("missing or invalid --version-code: the bundle versionCode whose generated APKs to list is required")
	}
	cols, err := generatedcmd.ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg, err := generatedcmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	lr, raw, err := generatedapks.List(rc.Ctx, httpClient, pkg, in.VersionCode)
	if err != nil {
		return nil, generatedcmd.Classify(pkg, err)
	}
	return Payload{Rows: generatedcmd.BuildRows(lr), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases generated list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the APKs Play generated from an uploaded AAB",
		Long: `List the split, standalone, and universal APKs (plus asset-pack slices and
recovery modules) Play generated and signed from the App Bundle uploaded under a
given versionCode. Each artifact carries an opaque downloadId to feed
` + "`gplay releases generated download`" + `.

--version-code is required (the resource is keyed by version). The human table
flattens the grouped-by-signing-key response to one row per artifact; --output
json passes the GeneratedApksListResponse through verbatim (ADR-0003).

This is a direct application-scoped read: it opens no Edit.`,
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
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the bundle versionCode whose generated APKs to list (required)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+generatedcmd.DefaultColumns()+")")
	return cmd
}
