package rollout

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// RunComplete drives orchestrator.Complete: ramp the latest release to a
// full rollout (userFraction=1.0, status=completed).
func RunComplete(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	return runState(rc, in, 0, "completed", orchestrator.Complete)
}

// NewCompleteCommand returns the cobra command for `gplay releases complete`.
func NewCompleteCommand(boot kernel.Boot) *cobra.Command {
	return newStateCommand(boot, "complete",
		"Complete the rollout on the latest release of a track (ramp to 100%)",
		`Ramp the latest release on --track to userFraction=1.0 and status=completed,
ending the staged rollout.

Targets the latest release on the track; when two releases coexist pass
--version-code N or --release-name <name> to pick one.`,
		RunComplete)
}
