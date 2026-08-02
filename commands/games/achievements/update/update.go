// Package update implements `gplay games achievements update <achievementId>`:
// replace an achievement configuration's metadata (achievementConfigurations.update,
// PUT). Writes affect the editable draft (no publish method, ADR-0033). Routine
// tier (ADR-0017): no --confirm, --dry-run rehearses and previews the body.
// MarkMutating gates it under GPLAY_READONLY.
//
// update is a full PUT replace: the body sent REPLACES the resource's draft, so
// for a partial edit fetch the current config (`view --output json`), edit it,
// and resend it with --from-json. The field flags send only what they name.
package update

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	ID     string
	Write  gamescmd.AchievementWrite
	DryRun bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := gamescmd.RequireID(in.ID, "missing achievement id: gplay games achievements update <achievementId>")
	if err != nil {
		return nil, err
	}
	body, err := gamescmd.BuildAchievementBody(rc.Stdin, in.Write, true)
	if err != nil {
		return nil, err
	}
	cols, err := gamescmd.ResolveAchievementColumns("")
	if err != nil {
		return nil, err
	}
	target := "achievement " + id
	if in.DryRun {
		return gamescmd.AchievementWritePayload{Verb: "update achievement", Target: target, DryRun: true, Body: body, Cols: cols}, nil
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	a, raw, err := games.UpdateAchievement(rc.Ctx, httpClient, id, body)
	if err != nil {
		return nil, gamescmd.Classify(id, err)
	}
	rc.Confirmf("updated achievement %s", id)
	return gamescmd.AchievementWritePayload{Verb: "update achievement", Target: target, Row: gamescmd.BuildAchievementRow(a), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games achievements update`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "update <achievementId>",
		Short: "Update an achievement configuration (draft)",
		Long: `Update an achievement configuration by its ID. The write affects the editable
draft; the published copy is read-only and there is no publish method (ADR-0033).

This is a full PUT replace — the body REPLACES the draft. For a partial edit,
fetch the current config with 'gplay games achievements view <id> --output json',
edit it, and resend it with --from-json. The field flags (--name, --description,
--type, --initial-state, --point-value, --steps-to-unlock; --locale, default
en-US) send only what they name; --from-json supplies a full body verbatim and
is mutually exclusive with them.

Routine write (no --confirm); rehearse with --dry-run (no HTTP — --output json
shows the request body). GPLAY_READONLY refuses the live write (exit 4).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ID = args[0]
			in.Write.PointValueSet = cmd.Flags().Changed("point-value")
			in.Write.StepsSet = cmd.Flags().Changed("steps-to-unlock")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Write.FromJSON, "from-json", "", "read a full AchievementConfiguration JSON body from this file (- for stdin); mutually exclusive with field flags")
	cmd.Flags().StringVar(&in.Write.Name, "name", "", "localized achievement name (under --locale)")
	cmd.Flags().StringVar(&in.Write.Description, "description", "", "localized achievement description (under --locale)")
	cmd.Flags().StringVar(&in.Write.Locale, "locale", "", "BCP 47 locale for --name/--description (default en-US)")
	cmd.Flags().StringVar(&in.Write.Type, "type", "", "achievement type: STANDARD or INCREMENTAL")
	cmd.Flags().StringVar(&in.Write.InitialState, "initial-state", "", "initial state: HIDDEN or REVEALED")
	cmd.Flags().IntVar(&in.Write.PointValue, "point-value", 0, "point value for the achievement")
	cmd.Flags().IntVar(&in.Write.StepsToUnlock, "steps-to-unlock", 0, "steps to unlock (INCREMENTAL achievements only)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "build and preview the request body without any HTTP call")
	return cmd
}
