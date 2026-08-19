// Package create implements `gplay games leaderboards create --application-id
// <id>`: insert a new leaderboard configuration (leaderboardConfigurations.insert).
// Writes affect the editable draft (there is no publish method, ADR-0033).
// ROUTINE tier (ADR-0017): no --confirm, --dry-run rehearses and previews the
// request body. MarkMutating gates it under GPLAY_READONLY. The payload is built
// from field flags or supplied whole with --from-json.
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
	Write         gamescmd.LeaderboardWrite
	DryRun        bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	appID, err := gamescmd.ResolveApplicationID(in.ApplicationID)
	if err != nil {
		return nil, err
	}
	body, err := gamescmd.BuildLeaderboardBody(rc.Stdin, in.Write, true)
	if err != nil {
		return nil, err
	}
	cols, err := gamescmd.ResolveLeaderboardColumns("")
	if err != nil {
		return nil, err
	}
	target := "application " + appID
	if in.DryRun {
		return gamescmd.LeaderboardWritePayload{Verb: "create leaderboard", Target: target, DryRun: true, Body: body, Cols: cols}, nil
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	l, raw, err := games.CreateLeaderboard(rc.Ctx, httpClient, appID, body)
	if err != nil {
		return nil, gamescmd.Classify(appID, err)
	}
	rc.Confirmf("created leaderboard %s on Play Games application %s", l.ID, appID)
	return gamescmd.LeaderboardWritePayload{Verb: "create leaderboard", Target: target, Row: gamescmd.BuildLeaderboardRow(l), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games leaderboards create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a leaderboard configuration (draft)",
		Long: `Create a new leaderboard configuration for a Play Games Services application.
The write affects the editable draft; the published copy is read-only and there
is no publish method: publishing to players is Console-only (ADR-0033).

Provide the leaderboard either field-by-field or whole:

  --name                    localized name (under --locale, default en-US)
  --score-order             LARGER_IS_BETTER | SMALLER_IS_BETTER
  --score-min / --score-max integer score bounds
  --from-json <file|->      a full LeaderboardConfiguration JSON body, passed
                            verbatim: the round-trip of 'view --output json',
                            and the way to set scoreFormat / multiple locales

--from-json and the field flags are mutually exclusive. A draft create is
routine (no --confirm); rehearse with --dry-run (no HTTP: --output json shows
the request body). GPLAY_READONLY refuses the live write (exit 4).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in.Write.ScoreMinSet = cmd.Flags().Changed("score-min")
			in.Write.ScoreMaxSet = cmd.Flags().Changed("score-max")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.ApplicationID, "application-id", "", "numeric Play Games Services application ID (required)")
	cmd.Flags().StringVar(&in.Write.FromJSON, "from-json", "", "read a full LeaderboardConfiguration JSON body from this file (- for stdin); mutually exclusive with field flags")
	cmd.Flags().StringVar(&in.Write.Name, "name", "", "localized leaderboard name (under --locale)")
	cmd.Flags().StringVar(&in.Write.Locale, "locale", "", "BCP 47 locale for --name (default en-US)")
	cmd.Flags().StringVar(&in.Write.ScoreOrder, "score-order", "", "score order: LARGER_IS_BETTER or SMALLER_IS_BETTER")
	cmd.Flags().Int64Var(&in.Write.ScoreMin, "score-min", 0, "minimum score that can be posted")
	cmd.Flags().Int64Var(&in.Write.ScoreMax, "score-max", 0, "maximum score that can be posted")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "build and preview the request body without any HTTP call")
	return cmd
}
