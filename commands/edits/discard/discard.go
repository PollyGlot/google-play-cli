// Package discard implements `gplay edits discard`: discard the explicit Edit
// pinned in .gplay/edit-<package>.json (edits.delete) and clear the pin. The
// pin is cleared REGARDLESS of the API outcome: an Edit that has already
// expired or vanished server-side (404) is the desired end state either way, so
// the local pin must not be left behind to block the next `edits begin`. With
// no open Edit it fails with exit 60.
package discard

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
}

// alreadyGone reports whether err is a 404 from edits.delete: the Edit is
// already gone (expired or discarded elsewhere), which is success for a
// discard: the end state we wanted.
func alreadyGone(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
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

	discardErr := edits.DiscardExplicit(rc.Ctx, httpClient, pkg, pin.EditID)
	// Clear the pin regardless of the API outcome: a 404 means the Edit is
	// already gone, and even on a transient error the user asked to abandon it
	// (it auto-expires in ~24h), so the local pin must not linger.
	clearErr := editpin.Clear(rc.FS, gplayDir, pkg)

	// An already-gone (404) Edit is the discard's desired end state, not a
	// failure. Combine the remaining errors so a local cleanup failure never
	// masks whether the Edit is still open on Play.
	remoteErr := discardErr
	if alreadyGone(remoteErr) {
		remoteErr = nil
	}
	switch {
	case remoteErr != nil && clearErr != nil:
		return nil, fmt.Errorf("discard explicit edit %s for %s: %w; also failed to clear the local pin: %v", pin.EditID, pkg, remoteErr, clearErr)
	case remoteErr != nil:
		return nil, remoteErr
	case clearErr != nil:
		return nil, fmt.Errorf("discarded explicit edit %s for %s on Play, but failed to clear the local pin %s (remove it manually): %w", pin.EditID, pkg, editpin.Path(gplayDir, pkg), clearErr)
	}

	rc.Confirmf("discarded explicit edit %s for %s", pin.EditID, pkg)
	return editscmd.Payload{Package: pkg, EditID: pin.EditID, Open: false, Action: "discarded"}, nil
}

// NewCommand returns the cobra command for `gplay edits discard`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "discard",
		Short: "Discard the open explicit Edit and clear its pin",
		Long: `Discard the explicit Edit pinned in .gplay/edit-<package>.json (the one
` + "`gplay edits begin`" + ` opened) and clear the pin.

The pin is cleared regardless of the API outcome: an Edit that has already
expired or been discarded server-side (404) is the end state a discard wants,
so the local pin is never left behind to block the next ` + "`gplay edits begin`" + `.

With no open Edit, discard fails with exit 60. The package defaults to the
repo's .gplay/config.json pin when --package is omitted.`,
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
