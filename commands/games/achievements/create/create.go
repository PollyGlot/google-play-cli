// Package create implements `gplay games achievements create --application-id
// <id>`: insert a new achievement configuration (achievementConfigurations.insert).
// Writes affect the editable draft (there is no publish method, ADR-0033).
// A draft create is the ROUTINE tier (ADR-0017): no --confirm, --dry-run
// rehearses and previews the request body. MarkMutating gates it under
// GPLAY_READONLY. The payload is built from field flags or supplied whole with
// --from-json (the ADR-0003 round-trip of `view --output json`).
package create

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	ApplicationID string
	Write         gamescmd.AchievementWrite
	DryRun        bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	appID, err := gamescmd.ResolveApplicationID(in.ApplicationID)
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
	target := "application " + appID
	if in.DryRun {
		return gamescmd.AchievementWritePayload{Verb: "create achievement", Target: target, DryRun: true, Body: body, Cols: cols}, nil
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	a, raw, err := games.CreateAchievement(rc.Ctx, httpClient, appID, body)
	if err != nil {
		return nil, gamescmd.Classify(appID, err)
	}
	rc.Confirmf("created achievement %s on Play Games application %s", a.ID, appID)
	return gamescmd.AchievementWritePayload{Verb: "create achievement", Target: target, Row: gamescmd.BuildAchievementRow(a), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games achievements create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an achievement configuration (draft)",
		Long: `Create a new achievement configuration for a Play Games Services application.
The write affects the editable draft; the published copy is read-only and there
is no publish method: publishing to players is Console-only (ADR-0033).

Provide the achievement either field-by-field or whole:

  --name / --description    localized strings (under --locale, default en-US)
  --type                    STANDARD | INCREMENTAL
  --initial-state           HIDDEN | REVEALED
  --point-value             integer point value
  --steps-to-unlock         steps (INCREMENTAL only)
  --from-json <file|->      a full AchievementConfiguration JSON body, passed
                            verbatim: the round-trip of 'view --output json',
                            and the way to set multiple locales at once

--from-json and the field flags are mutually exclusive. A draft create is
routine (no --confirm); rehearse with --dry-run (no HTTP: --output json shows
the request body). GPLAY_READONLY refuses the live write (exit 4).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in.Write.PointValueSet = cmd.Flags().Changed("point-value")
			in.Write.StepsSet = cmd.Flags().Changed("steps-to-unlock")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.ApplicationID, "application-id", "", "numeric Play Games Services application ID (required)")
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
