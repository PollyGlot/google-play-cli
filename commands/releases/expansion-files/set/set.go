// Package set implements `gplay releases expansion-files set`: declaratively
// point an APK's expansion file at another APK versionCode's already-uploaded
// file (no new binary). It wraps edits.expansionfiles.update (PUT) inside the
// Edit lifecycle. `set` covers both the API's update (PUT) and patch (PATCH) —
// a 1-writable-field resource needs one declarative verb (ADR-0019 / ADR-0030).
// ROUTINE tier: MarkMutating, --dry-run, no --confirm. Ships [experimental].
package set

import (
	"encoding/json"
	"fmt"
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
	Package           string
	VersionCode       int
	Type              string
	ReferencesVersion int
	KeepEditOnFailure bool
	DryRun            bool
}

// Payload renders the set (live) or rehearsal (--dry-run).
type Payload struct {
	VersionCode       int
	Type              string
	ReferencesVersion int
	DryRun            bool
	Raw               json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			verb := "set"
			if p.DryRun {
				verb = "would set"
			}
			_, err := fmt.Fprintf(w, "%s versionCode %d %s expansion file to reference versionCode %d%s\n", verb, p.VersionCode, p.Type, p.ReferencesVersion, dryRunSuffix(p.DryRun))
			return err
		},
		JSON: func(w io.Writer) error {
			if p.DryRun {
				return output.WriteJSON(w, map[string]any{"dryRun": true, "versionCode": p.VersionCode, "type": p.Type, "referencesVersion": p.ReferencesVersion})
			}
			_, err := w.Write(p.Raw)
			return err
		},
		Markdown: func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "- **versionCode**: %d\n- **type**: %s\n- **referencesVersion**: %d\n", p.VersionCode, p.Type, p.ReferencesVersion)
			return err
		},
	}
}

func dryRunSuffix(dry bool) string {
	if dry {
		return " (dry-run)"
	}
	return ""
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.VersionCode <= 0 {
		return nil, expansionfilescmd.Usagef("missing or invalid --version-code: the APK whose expansion config is being set is required")
	}
	if in.ReferencesVersion <= 0 {
		return nil, expansionfilescmd.Usagef("missing or invalid --references-version: the APK versionCode whose expansion file to reference is required")
	}
	ft, err := expansionfilescmd.NormalizeType(in.Type)
	if err != nil {
		return nil, err
	}
	pkg, err := expansionfilescmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	if in.DryRun {
		return Payload{VersionCode: in.VersionCode, Type: ft, ReferencesVersion: in.ReferencesVersion, DryRun: true}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-set Edit.
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	err = edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID}, func(editID string) error {
		r, e := expansionfiles.Update(rc.Ctx, httpClient, pkg, editID, in.VersionCode, ft, in.ReferencesVersion)
		raw = r
		return e
	})
	if err != nil {
		return nil, err
	}
	rc.Confirmf("set versionCode %d %s expansion file to reference versionCode %d for %q", in.VersionCode, ft, in.ReferencesVersion, pkg)
	return Payload{VersionCode: in.VersionCode, Type: ft, ReferencesVersion: in.ReferencesVersion, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases expansion-files set`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "[experimental] Point an APK's expansion file at another APK's file",
		Long: `Declaratively set an APK's expansion file configuration to reference another
APK versionCode's already-uploaded expansion file (no new binary uploaded).
Wraps edits.expansionfiles.update (PUT) inside the Edit lifecycle.

LEGACY (OBB). --type is main or patch; --references-version is the APK
versionCode whose expansion file to point at. --dry-run validates inputs
without any HTTP call. GPLAY_READONLY refuses it.

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
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "the APK versionCode whose expansion config is being set (required)")
	cmd.Flags().StringVar(&in.Type, "type", "main", "expansion file type: main or patch")
	cmd.Flags().IntVar(&in.ReferencesVersion, "references-version", 0, "the APK versionCode whose already-uploaded expansion file to reference (required)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs without any HTTP call")
	return cmd
}
