package remove

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

type Input struct {
	ID      string
	Confirm bool
	DryRun  bool
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := gamescmd.RequireID(in.ID, "missing leaderboard id: gplay games leaderboards remove <leaderboardId>")
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		return gamescmd.DeletePayload{Kind: "leaderboard", ID: id, DryRun: true}, nil
	}
	if err := gamescmd.RequireConfirm(in.Confirm, "deleting a leaderboard configuration is irreversible; pass --confirm to proceed (rehearse first with --dry-run)"); err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	if err := games.DeleteLeaderboard(rc.Ctx, httpClient, id); err != nil {
		return nil, gamescmd.Classify(id, err)
	}
	rc.Confirmf("deleted leaderboard %s", id)
	return gamescmd.DeletePayload{Kind: "leaderboard", ID: id}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "remove <leaderboardId>",
		Short: "Remove a leaderboard configuration (irreversible delete)",
		Long: `Remove (delete) a leaderboard configuration by its ID. This is irreversible.

Requires --confirm (missing → exit 3); CI=true never auto-confirms. Rehearse
first with --dry-run. GPLAY_READONLY refuses the live delete (exit 4).`,
		Example: `  gplay games leaderboards remove CgkI4ZqN0pMUEAIQAg --dry-run
  gplay games leaderboards remove CgkI4ZqN0pMUEAIQAg --confirm`,
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
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the irreversible deletion")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse without any HTTP call (reports the --confirm requirement)")
	return cmd
}
