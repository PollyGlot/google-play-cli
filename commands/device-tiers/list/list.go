// Package list implements `gplay device-tiers list`: list the app's device tier
// configs (newest first). Read-only, no Edit transaction. --page-size/--page-token
// expose pagination; --output json passes ListDeviceTierConfigsResponse through
// verbatim (including nextPageToken).
package list

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/device-tiers/devicetierscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package   string
	PageSize  int
	PageToken string
	Columns   string
}

// Payload renders the configs as a table / verbatim JSON.
type Payload struct {
	Rows []devicetierscmd.Row
	Cols []output.Column[devicetierscmd.Row]
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
	cols, err := devicetierscmd.ResolveColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	pkg, err := devicetierscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	lr, raw, err := devicetiers.List(rc.Ctx, httpClient, pkg, in.PageSize, in.PageToken)
	if err != nil {
		return nil, devicetierscmd.Classify(pkg, err)
	}
	return Payload{Rows: devicetierscmd.BuildRows(lr.DeviceTierConfigs), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay device-tiers list`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "[experimental] List the app's device tier configs (newest first)",
		Long: `List the app's device tier configs, newest first. Use --page-size and
--page-token to page; --output json passes the ListDeviceTierConfigsResponse
through verbatim, including nextPageToken (ADR-0003).

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
	cmd.Flags().IntVar(&in.PageSize, "page-size", 0, "max configs per page (API default when unset; capped at 100)")
	cmd.Flags().StringVar(&in.PageToken, "page-token", "", "page token from a previous response's nextPageToken")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+devicetierscmd.DefaultColumns()+")")
	return cmd
}
