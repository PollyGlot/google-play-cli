// Package view implements `gplay device-tiers view <deviceTierConfigId>`: read a
// single device tier config by its server-assigned int64 id. Read-only, no Edit
// transaction. --output json passes the DeviceTierConfig through verbatim.
package view

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/device-tiers/devicetierscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	Package string
	ID      string
	Columns string
}

// Payload renders one config as a single-row table / verbatim JSON.
type Payload struct {
	Row  devicetierscmd.Row
	Cols []output.Column[devicetierscmd.Row]
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Cols, []devicetierscmd.Row{p.Row}) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []devicetierscmd.Row{p.Row}) },
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if strings.TrimSpace(in.ID) == "" {
		return nil, devicetierscmd.Usagef("missing device tier config id: gplay device-tiers view <id>")
	}
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
	cfg, raw, err := devicetiers.Get(rc.Ctx, httpClient, pkg, in.ID)
	if err != nil {
		return nil, devicetierscmd.Classify(pkg, err)
	}
	return Payload{Row: devicetierscmd.BuildRow(cfg), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay device-tiers view`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <deviceTierConfigId>",
		Short: "[experimental] Read one device tier config by id",
		Long: `Read a single device tier config by its server-assigned id.
--output json passes the DeviceTierConfig through verbatim (ADR-0003).

[experimental] — the surface may still evolve (ADR-0010).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ID = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+devicetierscmd.DefaultColumns()+")")
	return cmd
}
