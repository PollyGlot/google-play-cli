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

type Input struct {
	Package string
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	gplayDir, err := editscmd.RequireGplayDir(rc)
	if err != nil {
		return nil, err
	}

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
		if discardErr := edits.DiscardExplicit(rc.Ctx, httpClient, pkg, editID); discardErr != nil {
			return nil, fmt.Errorf("write edit pin: %w; cleanup also failed: explicit edit %s is still open on Play (discard it via the Play Console or wait ~24h): %v", err, editID, discardErr) //nolint:errorlint // secondary error must not own the exit code
		}
		return nil, err
	}

	rc.Confirmf("opened explicit edit %s for %s: run `gplay edits commit` to publish, or `gplay edits discard` to abandon", editID, pkg)
	return editscmd.Payload{Package: pkg, EditID: editID, Open: true, Action: "began"}, nil
}

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
of opening their own, so several changes batch into one atomic transaction.

The Edit stays open until you run ` + "`gplay edits commit`" + ` (publish) or
` + "`gplay edits discard`" + ` (abandon). There is no auto-commit and no
auto-discard in explicit mode: the lifecycle is yours.

The package defaults to the repo's .gplay/config.json pin when --package is
omitted; a project (gplay init) is required since the pin lives in .gplay/.
Opening a second Edit while one is already pinned is refused (exit 60).`,
		Example: `  # Open one Edit, run write commands (they join it), then gplay edits commit
  gplay edits begin

  # Name the package explicitly and keep the Edit id for a script
  gplay edits begin --package com.example.app --output json`,
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
