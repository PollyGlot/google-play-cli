// Package status implements `gplay edits status`: report the open explicit Edit
// pinned in .gplay/edit-<package>.json, or "no open explicit edit" when none is
// pinned. It is a purely LOCAL read (no auth, no network) so it works offline
// and never provokes a credential probe.
package status

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/editscmd"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
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
	return editscmd.Payload{Package: pkg, EditID: pin.EditID, Open: ok}, nil
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

This is a local read (no auth, no network) so it works offline. The package
defaults to the repo's .gplay/config.json pin when --package is omitted.

--output json emits {"package","editId","open"}.`,
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
