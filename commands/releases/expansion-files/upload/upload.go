// Package upload implements `gplay releases expansion-files upload`: upload a
// legacy .obb expansion file and attach it to an already-published APK
// versionCode, through the full Edit lifecycle (insert → upload → commit). OBB
// is legacy (superseded by Play Asset Delivery for AABs). ROUTINE tier
// (ADR-0017): MarkMutating, --dry-run, no --confirm. Ships [experimental].
package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/expansionfilescmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
)

// Input is the request-shaped struct cobra builds from flags + args.
type Input struct {
	Package           string
	VersionCode       int
	Type              string
	OBBPath           string
	KeepEditOnFailure bool
	DryRun            bool
}

type localIOError struct {
	path  string
	cause error
}

func (e *localIOError) Error() string { return fmt.Sprintf("%s: %v", e.path, e.cause) }
func (e *localIOError) Unwrap() error { return e.cause }
func (e *localIOError) ExitCode() int { return 20 }

// Payload renders the upload (live) or rehearsal (--dry-run).
type Payload struct {
	VersionCode int
	Type        string
	DryRun      bool
	Raw         json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table: func(w io.Writer) error {
			if p.DryRun {
				_, err := fmt.Fprintf(w, "would upload a %s expansion file to versionCode %d (dry-run)\n", p.Type, p.VersionCode)
				return err
			}
			_, err := fmt.Fprintf(w, "versionCode:  %d\ntype:         %s\n", p.VersionCode, p.Type)
			return err
		},
		JSON: func(w io.Writer) error {
			if p.DryRun {
				return output.WriteJSON(w, map[string]any{"dryRun": true, "versionCode": p.VersionCode, "type": p.Type})
			}
			_, err := w.Write(p.Raw)
			return err
		},
		Markdown: func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "- **versionCode**: %d\n- **type**: %s\n", p.VersionCode, p.Type)
			return err
		},
	}
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.VersionCode <= 0 {
		return nil, expansionfilescmd.Usagef("missing or invalid --version-code: the APK versionCode the expansion file attaches to is required")
	}
	if in.OBBPath == "" {
		return nil, expansionfilescmd.Usagef("missing .obb path: gplay releases expansion-files upload <file.obb> ...")
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
		// Validate the local file offline (regular-file check) so a bad path
		// fails at exit 20 without any HTTP, then preview.
		info, statErr := os.Stat(in.OBBPath)
		if statErr != nil {
			return nil, &localIOError{path: in.OBBPath, cause: statErr}
		}
		if !info.Mode().IsRegular() {
			return nil, &localIOError{path: in.OBBPath, cause: fmt.Errorf("not a regular file")}
		}
		return Payload{VersionCode: in.VersionCode, Type: ft, DryRun: true}, nil
	}

	// UploadClient: a >150 MB .obb is exempt from the control-plane timeout.
	httpClient, err := rc.UploadClient()
	if err != nil {
		return nil, err
	}
	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-upload Edit.
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	err = edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID}, func(editID string) error {
		r, e := expansionfiles.Upload(rc.Ctx, httpClient, pkg, editID, in.VersionCode, ft, in.OBBPath)
		raw = r
		return e
	})
	if err != nil {
		return nil, err
	}
	rc.Confirmf("uploaded %s expansion file to versionCode %d for %q", ft, in.VersionCode, pkg)
	return Payload{VersionCode: in.VersionCode, Type: ft, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay releases expansion-files upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "upload <file.obb>",
		Short: "[experimental] Upload a legacy OBB expansion file to an APK versionCode",
		Long: `Upload a legacy .obb expansion file and attach it to an already-published
APK versionCode, via the full Edit lifecycle (insert → upload → commit;
auto-discarded on failure unless --keep-edit-on-failure).

LEGACY: expansion files are the pre-AAB mechanism for >150 MB out-of-APK
assets — most apps use Play Asset Delivery instead. Only APK-based apps use OBB.

--type is main or patch (the two expansion files per APK). --dry-run validates
the local file and inputs without any HTTP call. GPLAY_READONLY refuses it.

[experimental] — the surface may still evolve (ADR-0010).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.OBBPath = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "the APK versionCode the expansion file attaches to (required)")
	cmd.Flags().StringVar(&in.Type, "type", "main", "expansion file type: main or patch")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and the local file without any HTTP call")
	return cmd
}
