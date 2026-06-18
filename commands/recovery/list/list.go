// Package list implements `gplay recovery list`: list the recovery actions for a
// given (bad) versionCode. Read-only. The API marks versionCode required, so
// gplay requires --version-code (a list with no version is semantically empty);
// missing it is CLI misuse (exit 2) caught before any network. There is no
// `recovery view` — the apprecovery resource exposes only list.
package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	VersionCode int64
	Columns     string
}

// Payload renders the recoveries as a table / verbatim JSON.
type Payload struct {
	Rows []recoverycmd.Row
	Cols []output.Column[recoverycmd.Row]
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
		return nil, recoverycmd.Usagef("missing or invalid --version-code: recovery actions are listed per versionCode")
	}
	cols, err := recoverycmd.ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg, err := recoverycmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	lr, raw, err := recovery.List(rc.Ctx, httpClient, pkg, in.VersionCode)
	if err != nil {
		return nil, recoverycmd.Classify(pkg, err)
	}
	return Payload{Rows: recoverycmd.BuildRows(lr.RecoveryActions), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay recovery list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "[experimental] List recovery actions for a versionCode",
		Long: `List the app recovery actions for a (bad) APK versionCode, with their id,
status, and creation time. --version-code is required (recoveries are keyed by
version); --output json passes the ListAppRecoveriesResponse through verbatim.

There is no ` + "`recovery view`" + ` — the API exposes only list.

[experimental] — the surface may still evolve (ADR-0010).`,
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
	cmd.Flags().Int64Var(&in.VersionCode, "version-code", 0, "the APK versionCode whose recovery actions to list (required)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+recoverycmd.DefaultColumns()+")")
	return cmd
}
