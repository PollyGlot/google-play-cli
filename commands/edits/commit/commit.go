// Package commit implements `gplay edits commit`: commit the explicit Edit
// pinned in .gplay/edit-<package>.json (edits.commit) and clear the pin on
// success. With no open Edit it fails with exit 60. A failed commit (e.g. a
// validation error from Google) leaves the Edit open AND the pin in place, so
// the user can fix the cause and re-attempt, or discard.
package commit

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

	pin, ok, err := editpin.Lookup(rc.FS, gplayDir, pkg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &editscmd.NoOpenEditError{Package: pkg}
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	if err := edits.CommitExplicit(rc.Ctx, httpClient, pkg, pin.EditID); err != nil {
		// Leave the pin in place: the Edit is still open, so a re-run can retry
		// the commit (or `gplay edits discard` can abandon it).
		return nil, err
	}
	if err := editpin.Clear(rc.FS, gplayDir, pkg); err != nil {
		// The Edit IS committed (the change is live); only the local pin cleanup
		// failed. Say so explicitly and name the stale file, so this does not
		// read as a failed commit and the leftover pin (which would block the
		// next `gplay edits begin`) can be removed by hand.
		return nil, fmt.Errorf("committed explicit edit %s for %s, but failed to clear the local pin %s (remove it manually): %w", pin.EditID, pkg, editpin.Path(gplayDir, pkg), err)
	}

	rc.Confirmf("committed explicit edit %s for %s", pin.EditID, pkg)
	return editscmd.Payload{Package: pkg, EditID: pin.EditID, Open: false, Action: "committed"}, nil
}

// NewCommand returns the cobra command for `gplay edits commit`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit the open explicit Edit and clear its pin",
		Long: `Commit the explicit Edit pinned in .gplay/edit-<package>.json (the one
` + "`gplay edits begin`" + ` opened) and clear the pin on success.

With no open Edit, commit fails with exit 60. If the commit itself fails (for
example a validation error from Google), the Edit stays open and the pin is
left in place — fix the cause and re-run, or ` + "`gplay edits discard`" + `.

The package defaults to the repo's .gplay/config.json pin when --package is
omitted.`,
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
