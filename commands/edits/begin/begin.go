// Package begin implements `gplay edits begin`: open an explicit Edit on a
// package and persist its ID to .gplay/edit-<package>.json, so subsequent write
// commands in the same project reuse it instead of opening their own (the
// explicit Edit lifecycle, docs/DESIGN.md §4). The Edit stays open until the
// user runs `gplay edits commit` or `gplay edits discard` — there is no
// auto-commit and no auto-discard in this mode.
//
// Thin glue: resolve --package, locate the project's .gplay/, refuse if an Edit
// is already pinned, open one (edits.insert) and write the pin. A second begin
// while one is open would orphan the first, so it is refused (exit 60).
package begin

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := editscmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	gplayDir, err := editscmd.RequireGplayDir(rc)
	if err != nil {
		return nil, err
	}

	// Refuse a second begin while an Edit is already pinned — opening another
	// would orphan the first (Edits are exclusive per app, one open at a time).
	if pin, ok, err := editpin.Lookup(rc.FS, gplayDir, pkg); err != nil {
		return nil, err
	} else if ok {
		return nil, &editscmd.AlreadyOpenError{Package: pkg, EditID: pin.EditID}
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	editID, err := edits.OpenExplicit(rc.Ctx, httpClient, pkg)
	if err != nil {
		return nil, err
	}
	if err := editpin.Write(rc.FS, gplayDir, pkg, editID); err != nil {
		// The Edit is open server-side but we could not persist the pin. Discard
		// it so the user is not left with an orphaned 24h Edit lock they cannot
		// see locally. If the discard ALSO fails, surface both — the Edit is
		// still open with no local pin to recover it.
		if discardErr := edits.DiscardExplicit(rc.Ctx, httpClient, pkg, editID); discardErr != nil {
			return nil, fmt.Errorf("write edit pin: %w; cleanup also failed — explicit edit %s is still open on Play (discard it via the Play Console or wait ~24h): %v", err, editID, discardErr)
		}
		return nil, err
	}

	rc.Confirmf("opened explicit edit %s for %s — run `gplay edits commit` to publish, or `gplay edits discard` to abandon", editID, pkg)
	return editscmd.Payload{Package: pkg, EditID: editID, Open: true, Action: "began"}, nil
}

// NewCommand returns the cobra command for `gplay edits begin`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "begin",
		Short: "Open an explicit Edit and pin it for subsequent write commands",
		Long: `Open an explicit Edit on --package and persist its ID to
.gplay/edit-<package>.json. While the pin exists, subsequent write commands
(releases upload, tracks create, metadata apply, …) reuse the open Edit instead
of opening their own — so several changes batch into one atomic transaction.

The Edit stays open until you run ` + "`gplay edits commit`" + ` (publish) or
` + "`gplay edits discard`" + ` (abandon). There is no auto-commit and no
auto-discard in explicit mode — the lifecycle is yours.

The package defaults to the repo's .gplay/config.json pin when --package is
omitted; a project (gplay init) is required since the pin lives in .gplay/.
Opening a second Edit while one is already pinned is refused (exit 60).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	return cmd
}
