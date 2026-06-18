// Package view implements `gplay releases expansion-files view`: read an APK's
// expansion file configuration (fileSize XOR referencesVersion) for a given
// versionCode and type. Read-only: it opens a read-only Edit, gets, and discards
// (insert → get → discard). LEGACY (OBB). Ships [experimental].
package view

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/expansionfilescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package     string
	VersionCode int
	Type        string
}

// Payload renders the expansion file config / verbatim JSON.
type Payload struct {
	VersionCode int
	Type        string
	File        expansionfiles.ExpansionFile
	Raw         json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			return expansionfilescmd.RenderExpansionFile(w, p.VersionCode, p.Type, p.File)
		},
		JSON: func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error {
			return expansionfilescmd.RenderExpansionFile(w, p.VersionCode, p.Type, p.File)
		},
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.VersionCode <= 0 {
		return nil, expansionfilescmd.Usagef("missing or invalid --version-code: the APK versionCode to read is required")
	}
	ft, err := expansionfilescmd.NormalizeType(in.Type)
	if err != nil {
		return nil, err
	}
	pkg, err := expansionfilescmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	var (
		ef  expansionfiles.ExpansionFile
		raw json.RawMessage
	)
	err = edits.WithReadOnlyEdit(rc.Ctx, httpClient, pkg, func(editID string) error {
		f, r, e := expansionfiles.Get(rc.Ctx, httpClient, pkg, editID, in.VersionCode, ft)
		ef, raw = f, r
		return e
	})
	if err != nil {
		return nil, err
	}
	return Payload{VersionCode: in.VersionCode, Type: ft, File: ef, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases expansion-files view`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view",
		Short: "[experimental] Read an APK's expansion file configuration",
		Long: `Read the expansion file configuration (fileSize if this APK has its own
uploaded file, or referencesVersion if it points at another APK's) for a given
versionCode and type. Opens a read-only Edit (insert → get → discard).

LEGACY (OBB). --type is main or patch. --output json passes the ExpansionFile
through verbatim (ADR-0003).

[experimental] — the surface may still evolve (ADR-0010).`,
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
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "the APK versionCode to read (required)")
	cmd.Flags().StringVar(&in.Type, "type", "main", "expansion file type: main or patch")
	return cmd
}
