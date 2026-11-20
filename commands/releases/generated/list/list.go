package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/generated/generatedcmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

type Input struct {
	Package     string
	VersionCode int64
	Columns     string
}

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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.VersionCode <= 0 {
		return nil, exit.Usagef("missing or invalid --version-code: the bundle versionCode whose generated APKs to list is required")
	}
	cols, err := generatedcmd.ResolveColumns(in.Columns)
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
	lr, raw, err := generatedapks.List(rc.Ctx, httpClient, pkg, in.VersionCode)
	if err != nil {
		return nil, generatedcmd.Classify(pkg, err)
	}
	return Payload{Rows: generatedcmd.BuildRows(lr), Cols: cols, Raw: raw}, nil
}

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
json passes the GeneratedApksListResponse through verbatim.

This is a direct application-scoped read: it opens no Edit.`,
		Example: `  gplay releases generated list --version-code 1042
  gplay releases generated list --version-code 1042 --columns type,downloadId
  gplay releases generated list --version-code 1042 --output json`,
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
