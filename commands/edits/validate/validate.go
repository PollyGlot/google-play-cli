// Package validate implements `gplay edits validate`: run Google's commit-time
// checks on the explicit Edit pinned in .gplay/edit-<package>.json
// (edits.validate) WITHOUT committing it. The Edit stays open whatever the
// outcome, so a batch of writes can be checked before `gplay edits commit`.
// With no open Edit it fails with exit 60, like commit.
package validate

import (
	"encoding/json"
	"io"

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

// Payload is the renderable: the shared edits envelope for the human views,
// plus the raw AppEdit the API returned, mirrored verbatim under --output json
// (ADR-0003): unlike begin/commit/discard/status, validate has an upstream
// body to pass through.
type Payload struct {
	editscmd.Payload
	Raw json.RawMessage
}

// Renderers satisfies output.Renderable.
func (p Payload) Renderers() output.Renderers {
	r := p.Payload.Renderers()
	r.JSON = func(w io.Writer) error {
		if len(p.Raw) > 0 {
			_, err := w.Write(p.Raw)
			return err
		}
		return output.WriteJSON(w, p.Payload)
	}
	return r
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

	raw, err := edits.ValidateExplicit(rc.Ctx, httpClient, pkg, pin.EditID)
	if err != nil {
		// The API's rejection (its error envelope, exit code per HTTP status)
		// is the answer; the Edit and the pin stay in place for a fix-and-retry.
		return nil, err
	}

	rc.Confirmf("validated explicit edit %s for %s", pin.EditID, pkg)
	return Payload{
		Payload: editscmd.Payload{Package: pkg, EditID: pin.EditID, Open: true, Action: "validated"},
		Raw:     raw,
	}, nil
}

// NewCommand returns the cobra command for `gplay edits validate`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the open explicit Edit without committing it",
		Long: `Run Google's commit-time checks on the explicit Edit pinned in
.gplay/edit-<package>.json (the one ` + "`gplay edits begin`" + ` opened) without
committing it. Exit 0 means the Edit would commit as it stands; otherwise
the API's rejection is reported with the exit code its HTTP status maps to
(see ` + "`gplay exit-codes`" + `). The Edit stays open either way: fix the
cause and re-run, ` + "`gplay edits commit`" + `, or ` + "`gplay edits discard`" + `.

With no open Edit, validate fails with exit 60. The package defaults to the
repo's .gplay/config.json pin when --package is omitted.

--output json mirrors the API's AppEdit response verbatim.`,
		Example: `  # Dry-run the commit: exit 0 means the pinned Edit would commit as it stands
  gplay edits validate

  gplay edits validate --package com.example.app --output json`,
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
