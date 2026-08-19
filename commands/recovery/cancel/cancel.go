// Package cancel implements `gplay recovery cancel <appRecoveryId>`: terminate an
// app recovery action. The action persists with status CANCELED and CANNOT be
// resumed: irreversible. DESTRUCTIVE tier (ADR-0017): requires --confirm
// (missing → exit 3), MarkMutating. `cancel` is a domain verb admitted under
// ADR-0019 §2 (see ADR-0030), not `remove`, since the resource is not deleted.
package cancel

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	Package string
	ID      string
	Confirm bool
	DryRun  bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.ID == "" {
		return nil, recoverycmd.Usagef("missing recovery id: gplay recovery cancel <appRecoveryId>")
	}
	pkg, err := recoverycmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		return recoverycmd.LifecyclePayload{Verb: "cancel", RecoveryID: in.ID, Package: pkg, DryRun: true}, nil
	}
	if err := recoverycmd.RequireConfirm(in.Confirm, "canceling a recovery is irreversible (it cannot be resumed); pass --confirm to proceed (rehearse first with --dry-run)"); err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := recovery.Cancel(rc.Ctx, httpClient, pkg, in.ID)
	if err != nil {
		return nil, recoverycmd.Classify(pkg, err)
	}
	rc.Confirmf("canceled recovery %s for %q", in.ID, pkg)
	return recoverycmd.LifecyclePayload{Verb: "cancel", RecoveryID: in.ID, Package: pkg, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay recovery cancel`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "cancel <appRecoveryId>",
		Short: "Cancel a recovery action (irreversible)",
		Long: `Cancel an app recovery action. The action persists with status CANCELED and
CANNOT be resumed: this is irreversible. To target users again you must create
a new recovery.

Requires --confirm (missing → exit 3); rehearse first with --dry-run.
GPLAY_READONLY refuses it (exit 4).`,
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
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the irreversible cancellation")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse without any HTTP call (reports the --confirm requirement)")
	return cmd
}
