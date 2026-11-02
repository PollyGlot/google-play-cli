package rollout

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// RunComplete drives orchestrator.Complete: ramp the latest release to a
// full rollout (status=completed, which carries no userFraction).
func RunComplete(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	return runState(rc, in, 0, "completed", orchestrator.Complete)
}

// NewCompleteCommand returns the cobra command for `gplay releases complete`.
func NewCompleteCommand(boot kernel.Boot) *cobra.Command {
	cmd := newStateCommand(boot, "complete",
		"Complete the rollout on the latest release of a track (ramp to 100%)",
		`Ramp the latest release on --track to status=completed, ending the staged
rollout: the release reaches every user, and carries no userFraction.
<<<<<<< HEAD

A track holds one completed release at most, so the release this replaces
(the previous full rollout) leaves the track in the same update, as in the
Play Console; its versionCodes are reported as superseded. Halted, other
inProgress and draft releases on the track are kept unchanged.
=======
>>>>>>> origin/main

Targets the latest release on the track; when two releases coexist pass
--version-code N or --release-name <name> to pick one.`,
		RunComplete)
	cmd.Example = `  # Ramp the production rollout to 100% of users
  gplay releases complete --track production --confirm

  # Complete one of two coexisting releases on a Closed track
  gplay releases complete --track qa-team --release-name 2.4.0`
	return cmd
}
