// Package delete implements `gplay games leaderboards delete <leaderboardId>`:
// delete a leaderboard configuration (leaderboardConfigurations.delete).
// DESTRUCTIVE tier (ADR-0017): requires --confirm (missing → exit 3), CI=true
// never auto-confirms, --dry-run rehearses. MarkMutating gates it under
// GPLAY_READONLY.
package delete

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/games/gamescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	ID      string
	Confirm bool
	DryRun  bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	id, err := gamescmd.RequireID(in.ID, "missing leaderboard id: gplay games leaderboards delete <leaderboardId>")
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

// NewCommand returns the cobra command for `gplay games leaderboards delete`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "delete <leaderboardId>",
		Short: "Delete a leaderboard configuration (irreversible)",
		Long: `Delete a leaderboard configuration by its ID. This is irreversible.

Requires --confirm (missing → exit 3); CI=true never auto-confirms. Rehearse
first with --dry-run. GPLAY_READONLY refuses the live delete (exit 4).`,
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
