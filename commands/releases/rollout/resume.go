package rollout

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// RunResume drives orchestrator.Resume: flip a halted release back to
// inProgress, leaving its userFraction unchanged.
func RunResume(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	return runState(rc, in, 0, orchestrator.Resume)
}

// NewResumeCommand returns the cobra command for `gplay releases resume`.
func NewResumeCommand(boot kernel.Boot) *cobra.Command {
	return newStateCommand(boot, "resume",
		"Resume a halted rollout on the latest release of a track",
		`Set the latest release on --track back to status=inProgress, continuing
the rollout at the userFraction it was halted at.

Targets the latest release on the track; when two releases coexist pass
--version-code N or --release-name <name> to pick one.`,
		RunResume)
}
