// Package addtargeting implements `gplay recovery add-targeting <appRecoveryId>`:
// widen the audience of a recovery action. The API's addTargeting is APPEND-ONLY
// — it can only add users/regions/sdk-levels, never narrow. Because it increases
// the force-update blast radius it is the DESTRUCTIVE tier (ADR-0017): requires
// --confirm (missing → exit 3), MarkMutating. `add-targeting` is a domain verb
// admitted under ADR-0019 §2 (see ADR-0030) — not `set` (it cannot replace).
package addtargeting

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/recovery/recoverycmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	Package   string
	ID        string
	AllUsers  bool
	Regions   []string
	SdkLevels []int64
	Confirm   bool
	DryRun    bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.ID == "" {
		return nil, recoverycmd.Usagef("missing recovery id: gplay recovery add-targeting <appRecoveryId>")
	}
	if !in.AllUsers && len(in.Regions) == 0 && len(in.SdkLevels) == 0 {
		return nil, recoverycmd.Usagef("missing targeting to add: pass one of --all-users, --regions, or --sdk-levels")
	}
	pkg, err := recoverycmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		return recoverycmd.LifecyclePayload{Verb: "add-targeting", RecoveryID: in.ID, Package: pkg, DryRun: true}, nil
	}
	if err := recoverycmd.RequireConfirm(in.Confirm, "widening targeting force-updates more users (and can only widen, never narrow); pass --confirm to proceed (rehearse first with --dry-run)"); err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	t := recovery.BuildTargeting(in.AllUsers, in.Regions, in.SdkLevels)
	raw, err := recovery.AddTargeting(rc.Ctx, httpClient, pkg, in.ID, t)
	if err != nil {
		return nil, recoverycmd.Classify(pkg, err)
	}
	rc.Confirmf("widened targeting on recovery %s for %q", in.ID, pkg)
	return recoverycmd.LifecyclePayload{Verb: "add-targeting", RecoveryID: in.ID, Package: pkg, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay recovery add-targeting`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "add-targeting <appRecoveryId>",
		Short: "[experimental] Widen a recovery's audience (append-only)",
		Long: `Widen the audience of an app recovery action by adding users, regions, or
Android SDK levels. This is APPEND-ONLY — it can only widen, never narrow; to
shrink the blast radius, cancel the recovery and create a new one.

Pass at least one of --all-users, --regions <CC,CC>, or --sdk-levels <N,N>.
Requires --confirm (missing → exit 3); rehearse first with --dry-run.
GPLAY_READONLY refuses it (exit 4).

[experimental] — the surface may still evolve (ADR-0010).`,
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
	cmd.Flags().BoolVar(&in.AllUsers, "all-users", false, "add all users to the recovery's audience")
	cmd.Flags().StringSliceVar(&in.Regions, "regions", nil, "add these CLDR region codes (e.g. US,FR)")
	cmd.Flags().Int64SliceVar(&in.SdkLevels, "sdk-levels", nil, "add these Android SDK levels (e.g. 30,31)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize widening the force-update audience")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse without any HTTP call (reports the --confirm requirement)")
	return cmd
}
