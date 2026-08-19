// Package mappings implements `gplay releases mappings upload`: attach a
// ProGuard/R8 deobfuscation file (a Mapping, per CONTEXT.md) to an
// already-published versionCode after the fact, so Play vitals can
// symbolicate that version's obfuscated crash stacks. The CLI glue
// resolves --package, validates --version-code, and hands a MappingOpts
// to internal/releases/orchestrator; the Edit lifecycle and HTTP live
// there and in internal/play/*.
package mappings

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	MappingPath       string
	VersionCode       int
	Type              string
	KeepEditOnFailure bool
	DryRun            bool
}

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Payload satisfies output.Renderable for the MappingResult.
type Payload struct {
	Result *orchestrator.MappingResult
}

// Renderers returns the per-Format renderers. The JSON form is API
// pass-through (the raw deobfuscationfiles.upload response, ADR-0003);
// the table/markdown forms are human-shaped summaries.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Result) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p.Result) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

func renderTable(w io.Writer, r *orchestrator.MappingResult) error {
	symbol := r.SymbolType
	if symbol == "" {
		symbol = r.FileType
	}
	_, err := fmt.Fprintf(w,
		"versionCode:  %d\ntype:         %s\nsymbolType:   %s\n",
		r.VersionCode, r.FileType, symbol,
	)
	return err
}

func renderJSON(w io.Writer, r *orchestrator.MappingResult) error {
	// API pass-through: emit the raw deobfuscationfiles.upload body (ADR-0003).
	if len(r.Raw) > 0 {
		_, err := w.Write(r.Raw)
		return err
	}
	// Fallback to the gplay MappingResult shape (e.g. on --dry-run, where
	// no upload happened).
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func renderMarkdown(w io.Writer, r *orchestrator.MappingResult) error {
	_, err := fmt.Fprintf(w,
		"- **versionCode**: %d\n- **type**: %s\n",
		r.VersionCode, r.FileType,
	)
	return err
}

// Run validates the inputs, resolves the package, builds an authenticated
// HTTP client, then hands off to orchestrator.UploadMapping.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.MappingPath == "" {
		return nil, &usageError{msg: "missing mapping path: gplay releases mappings upload <mapping.txt> --version-code N"}
	}
	if in.VersionCode <= 0 {
		return nil, &usageError{msg: "missing or invalid --version-code (the APK versionCode to attach the mapping to)"}
	}

	// Resolve package: --package flag → project pin.
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}

	// Dry-run skips auth AND the explicit-Edit pin entirely: nothing hits the
	// network and the dry-run path never reuses a pinned Edit, so a corrupt pin
	// must not fail a preview. Both are resolved only on the live path.
	var (
		httpClient     *http.Client
		explicitEditID string
	)
	if !in.DryRun {
		var err error
		// UploadClient, not AuthedClient: native symbol archives can reach
		// the API's 1.6 GiB cap, so the upload is exempt from the 60s
		// control-plane default (honors an explicit --timeout).
		if httpClient, err = rc.UploadClient(); err != nil {
			return nil, err
		}
		// Reuse an open explicit Edit when one is pinned (`gplay edits begin`);
		// "" keeps the implicit per-upload Edit.
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
	}

	result, err := orchestrator.UploadMapping(rc.Ctx, httpClient, orchestrator.MappingOpts{
		Package:           pkg,
		VersionCode:       in.VersionCode,
		MappingPath:       in.MappingPath,
		FileType:          in.Type,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		DryRun:            in.DryRun,
	})
	if err != nil {
		return nil, err
	}
	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on
	// a --dry-run).
	if !in.DryRun {
		rc.ConfirmMutation(explicitEditID, "uploaded %s mapping for versionCode %d", result.FileType, result.VersionCode)
	}
	return Payload{Result: result}, nil
}

// NewCommand returns the cobra command for `gplay releases mappings upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "upload <mapping.txt>",
		Short: "Upload a ProGuard/R8 mapping for an already-published version",
		Long: `Attach a ProGuard/R8 deobfuscation file (mapping.txt) to an
already-published versionCode, after the fact, so Play vitals can
symbolicate that version's obfuscated crash stacks.

Performs the full Edit lifecycle in one call:
  edits.insert → deobfuscationfiles.upload → edits.commit

To upload a mapping at the same time as the AAB (the common case), pass
--mapping to gplay releases upload instead.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.MappingPath = args[0]
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().IntVar(&in.VersionCode, "version-code", 0, "APK versionCode the mapping belongs to (required)")
	cmd.Flags().StringVar(&in.Type, "type", "proguard", "deobfuscation file type: proguard or nativeCode")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs without any HTTP call")
	return cmd
}
