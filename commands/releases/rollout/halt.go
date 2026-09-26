package rollout

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// RunHalt drives orchestrator.Halt: freeze the latest release's rollout
// (status=halted) while preserving its userFraction.
func RunHalt(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	return runState(rc, in, 0, "halted", orchestrator.Halt)
}

// NewHaltCommand returns the cobra command for `gplay releases halt`.
func NewHaltCommand(boot kernel.Boot) *cobra.Command {
	cmd := newStateCommand(boot, "halt",
		"Halt the staged rollout on the latest release of a track",
		`Set the latest release on --track to status=halted, preserving its
current userFraction so a later `+"`gplay releases resume`"+` picks up where it
left off.

Targets the latest release on the track; when two releases coexist pass
--version-code N or --release-name <name> to pick one.`,
		RunHalt)
	cmd.Example = `  # Freeze a production rollout that is going wrong (no --confirm: halting reaches no new user)
  gplay releases halt --track production

  # Pick one of two coexisting releases and preview the transition
  gplay releases halt --track production --version-code 1042 --dry-run`
	return cmd
}
