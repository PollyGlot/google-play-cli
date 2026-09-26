// Package status implements `gplay edits status`: report the open explicit Edit
// pinned in .gplay/edit-<package>.json, or "no open explicit edit" when none is
// pinned. By default it is a purely LOCAL read (no auth, no network) so it
// works offline and never provokes a credential probe. --live adds one
// edits.get call on the pinned id to check the Edit still exists server-side
// (Edits expire after ~24h and may be discarded by another client).
package status

import (
	"errors"
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
	Live    bool
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
	payload := editscmd.Payload{Package: pkg, EditID: pin.EditID, Open: ok}
	// No pin means nothing to probe: --live still answers offline, so a CI
	// step can pass the flag unconditionally without paying an auth round-trip
	// when there is no Edit to check.
	if !in.Live || !ok {
		return payload, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	payload.Live = true
	appEdit, _, err := edits.GetExplicit(rc.Ctx, httpClient, pkg, pin.EditID)
	if err != nil {
		// 404 is an answer, not a failure: the pin outlived the Edit. Report
		// it as closed and leave the pin alone; clearing it is the user's
		// call via `gplay edits discard` (which tolerates a vanished Edit).
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			payload.Open = false
			payload.Gone = true
			return payload, nil
		}
		return nil, err
	}
	payload.ExpiryTimeSeconds = appEdit.ExpiryTimeSeconds
	return payload, nil
}

// NewCommand returns the cobra command for `gplay edits status`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the open explicit Edit for a package (or none)",
		Long: `Report the open explicit Edit pinned in .gplay/edit-<package>.json, or
"no open explicit edit" when none is pinned.

By default this is a local read (no auth, no network) so it works offline.
With --live, the pinned Edit is also looked up server-side (edits.get): the
report gains its expiry, or says the Edit no longer exists (expired, or
discarded by another client) and points at ` + "`gplay edits discard`" + ` to
clear the stale pin. --live without a pin stays offline.

The package defaults to the repo's .gplay/config.json pin when --package is
omitted.

--output json emits {"package","editId","open"}; with --live it adds
"live": true and, for an Edit the server still knows, "expiryTimeSeconds".`,
		Example: `  # Is an explicit Edit pinned here? (offline)
  gplay edits status

  # Also ask Google whether it is still alive, and when it expires
  gplay edits status --live --output json`,
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
	cmd.Flags().BoolVar(&in.Live, "live", false, "Also check the pinned Edit server-side (edits.get; needs auth)")
	return cmd
}
