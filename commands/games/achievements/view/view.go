// Package view implements `gplay games achievements view <achievementId>`: read
// a single achievement configuration (achievementConfigurations.get). Read-only.
// Each config carries an editable draft and a read-only published detail; the
// table summarises the best-available one. --output json passes the
// AchievementConfiguration through verbatim (ADR-0003). Ships [experimental].
package view

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	ID      string
	Columns string
}

// Payload renders one achievement as a single-row table / verbatim JSON.
type Payload struct {
	Row  gamescmd.AchievementRow
	Cols []output.Column[gamescmd.AchievementRow]
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, p.Cols, []gamescmd.AchievementRow{p.Row}) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, p.Cols, []gamescmd.AchievementRow{p.Row}) },
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := gamescmd.RequireID(in.ID, "missing achievement id: gplay games achievements view <achievementId>")
	if err != nil {
		return nil, err
	}
	cols, err := gamescmd.ResolveAchievementColumns(in.Columns)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	a, raw, err := games.GetAchievement(rc.Ctx, httpClient, id)
	if err != nil {
		return nil, gamescmd.Classify(id, err)
	}
	return Payload{Row: gamescmd.BuildAchievementRow(a), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games achievements view`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <achievementId>",
		Short: "[experimental] Read one achievement configuration by id",
		Long: `Read a single achievement configuration by its ID. The config carries an
editable draft and a read-only published detail (there is no publish method —
publishing to players is Console-only, ADR-0033). --output json passes the
AchievementConfiguration through verbatim (ADR-0003).

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
	cmd.Flags().StringVar(&in.Columns, "columns", "", "comma-separated table columns (default: "+gamescmd.DefaultAchievementColumns()+")")
	return cmd
}
