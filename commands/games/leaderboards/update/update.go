// Package update implements `gplay games leaderboards update <leaderboardId>`:
// replace a leaderboard configuration's metadata (leaderboardConfigurations.update,
// PUT). Writes affect the editable draft (no publish method, ADR-0033). Routine
// tier (ADR-0017): no --confirm, --dry-run rehearses and previews the body.
// MarkMutating gates it under GPLAY_READONLY.
//
// update is a full PUT replace: for a partial edit fetch the current config
// (`view --output json`), edit it, and resend it with --from-json.
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
	Write  gamescmd.LeaderboardWrite
	DryRun bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := gamescmd.RequireID(in.ID, "missing leaderboard id: gplay games leaderboards update <leaderboardId>")
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
	target := "leaderboard " + id
	if in.DryRun {
		return gamescmd.LeaderboardWritePayload{Verb: "update leaderboard", Target: target, DryRun: true, Body: body, Cols: cols}, nil
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	l, raw, err := games.UpdateLeaderboard(rc.Ctx, httpClient, id, body)
	if err != nil {
		return nil, gamescmd.Classify(id, err)
	}
	rc.Confirmf("updated leaderboard %s", id)
	return gamescmd.LeaderboardWritePayload{Verb: "update leaderboard", Target: target, Row: gamescmd.BuildLeaderboardRow(l), Cols: cols, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay games leaderboards update`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "update <leaderboardId>",
		Short: "Update a leaderboard configuration (draft)",
		Long: `Update a leaderboard configuration by its ID. The write affects the editable
draft; the published copy is read-only and there is no publish method (ADR-0033).

This is a full PUT replace: the body REPLACES the draft. For a partial edit,
fetch the current config with 'gplay games leaderboards view <id> --output json',
edit it, and resend it with --from-json. The field flags (--name, --score-order,
--score-min, --score-max; --locale, default en-US) send only what they name;
--from-json supplies a full body verbatim and is mutually exclusive with them.

Routine write (no --confirm); rehearse with --dry-run (no HTTP: --output json
shows the request body). GPLAY_READONLY refuses the live write (exit 4).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.ID = args[0]
			in.Write.ScoreMinSet = cmd.Flags().Changed("score-min")
			in.Write.ScoreMaxSet = cmd.Flags().Changed("score-max")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Write.FromJSON, "from-json", "", "read a full LeaderboardConfiguration JSON body from this file (- for stdin); mutually exclusive with field flags")
	cmd.Flags().StringVar(&in.Write.Name, "name", "", "localized leaderboard name (under --locale)")
	cmd.Flags().StringVar(&in.Write.Locale, "locale", "", "BCP 47 locale for --name (default en-US)")
	cmd.Flags().StringVar(&in.Write.ScoreOrder, "score-order", "", "score order: LARGER_IS_BETTER or SMALLER_IS_BETTER")
	cmd.Flags().Int64Var(&in.Write.ScoreMin, "score-min", 0, "minimum score that can be posted")
	cmd.Flags().Int64Var(&in.Write.ScoreMax, "score-max", 0, "maximum score that can be posted")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "build and preview the request body without any HTTP call")
	return cmd
}
