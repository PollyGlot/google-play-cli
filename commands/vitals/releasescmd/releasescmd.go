// Package releasescmd implements `gplay vitals releases`: the tracks, serving
// releases and version codes the Play Developer Reporting service holds vitals
// data for (apps.fetchReleaseFilterOptions, #348). It answers "which
// --version-code can I filter on?" before a preset is run. Read-only, on the
// same least-privilege reporting scope as the rest of `vitals`.
package releasescmd

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
}

// Payload renders the release filter options. JSON is the verbatim API
// pass-through (ADR-0003); table/markdown show one row per serving release.
type Payload struct {
	Raw      json.RawMessage
	Releases []vitals.Release
}

var columns = []output.Column[vitals.Release]{
	{Key: "track", Header: "TRACK", Value: func(r vitals.Release) string { return r.Track }},
	{Key: "type", Header: "TYPE", Value: func(r vitals.Release) string { return r.TrackType }},
	{Key: "release", Header: "RELEASE", Value: func(r vitals.Release) string { return r.Release }},
	{Key: "version_codes", Header: "VERSION_CODES", Value: func(r vitals.Release) string { return strings.Join(r.VersionCodes, ", ") }},
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return output.RenderTable(w, columns, p.Releases) },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p.Raw) },
		Markdown: func(w io.Writer) error { return output.RenderMarkdown(w, columns, p.Releases) },
	}
}

// Run resolves the package, fetches its release filter options and projects
// them for the table/markdown renderers.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	hc, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := vitals.FetchReleaseFilterOptions(rc.Ctx, hc, pkg)
	if err != nil {
		return nil, err
	}
	rows, err := vitals.ParseReleases(raw)
	if err != nil {
		return nil, err
	}
	return Payload{Raw: raw, Releases: rows}, nil
}

// NewCommand returns the cobra command for `gplay vitals releases`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "releases",
		Short: "List the tracks, releases and version codes that carry vitals data",
		Long: `List the tracks, their serving releases, and the version codes those releases
contain, as the Play Developer Reporting service knows them. These are the
values --version-code accepts on the vitals presets and ` + "`vitals errors counts`" + `,
which also complete them from this same call in a shell with completion set up.

Read-only; --output json mirrors the API response verbatim; table/markdown show
one row per serving release (a track serving none still gets a row).`,
		Example: `  gplay vitals releases --package com.example.app
  gplay vitals releases --output json`,
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
