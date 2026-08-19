// Package upload implements `gplay releases upload`: the CLI glue that
// resolves --package, parses the status flags (per ADR-0002), and hands
// an Opts payload to internal/releases/orchestrator. All the actual
// HTTP and Edit-lifecycle work lives in the orchestrator and its
// internal/play/* dependencies.
package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package           string
	Track             string
	AABPath           string
	Format            string // "" | "apk" | "bundle": overrides extension auto-detect
	Mapping           string
	ReleaseNotes      string
	ReleaseNotesDir   string
	Draft             bool
	Complete          bool
	StagedFraction    float64
	StagedFractionSet bool
	KeepEditOnFailure bool
	Confirm           bool
	DryRun            bool
}

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// resolveFormat classifies the artifact as an APK or an AAB, from an
// explicit --format override or the file extension (.apk / .aab). It
// mirrors the `releases sharing upload` convention (ADR-0030), including
// its exact "cannot tell APK from AAB by extension" message, but as a
// CLI-misuse usage error (exit 2), matching this command's flag-validation
// taxonomy. Returns the orchestrator format value (FormatAPK / FormatBundle).
func resolveFormat(path, formatOverride string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(formatOverride)) {
	case "apk":
		return orchestrator.FormatAPK, nil
	case "bundle":
		return orchestrator.FormatBundle, nil
	case "":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".apk":
			return orchestrator.FormatAPK, nil
		case ".aab":
			return orchestrator.FormatBundle, nil
		default:
			return "", &usageError{msg: "cannot tell APK from AAB by extension: pass --format apk|bundle"}
		}
	default:
		return "", &usageError{msg: "--format must be apk or bundle"}
	}
}

// Payload satisfies output.Renderable for the resulting upload Result.
type Payload struct {
	Result *orchestrator.Result
}

// Renderers returns the per-Format renderers. The JSON form is API
// pass-through (the raw tracks.update response, per ADR-0003); the
// table form is a human-shaped summary.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p.Result) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p.Result) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p.Result) },
	}
}

// showsUserFraction reports whether userFraction is semantically
// meaningful for the given release status AND value. inProgress always
// shows it (the fraction IS the rollout percentage); completed only
// shows it when non-zero (the API can omit userFraction on completed
// responses for 100% rollouts, and we should not surface a misleading
// "userFraction: 0" in that case). Draft / unspecified statuses always
// hide it: matches the JSON view's omitempty contract.
func showsUserFraction(status string, userFraction float64) bool {
	if status == "inProgress" {
		return true
	}
	if status == "completed" && userFraction > 0 {
		return true
	}
	return false
}

func renderTable(w io.Writer, r *orchestrator.Result) error {
	if _, err := fmt.Fprintf(w,
		"versionCode:  %d\ntrack:        %s\nstatus:       %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.Status, r.UserFraction) {
		if _, err := fmt.Fprintf(w, "userFraction: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "releaseName:  %s\n", r.ReleaseName); err != nil {
		return err
	}
	if r.DefaultLanguage != "" {
		if _, err := fmt.Fprintf(w, "defaultLang:  %s\n", r.DefaultLanguage); err != nil {
			return err
		}
	}
	if len(r.Locales) > 0 {
		if _, err := fmt.Fprintf(w, "locales:      %v\n", r.Locales); err != nil {
			return err
		}
	}
	return nil
}

func renderJSON(w io.Writer, r *orchestrator.Result) error {
	// API pass-through: emit the raw tracks.update body (ADR-0003).
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	// Fallback to the gplay Result shape if we somehow lost the raw.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func renderMarkdown(w io.Writer, r *orchestrator.Result) error {
	if _, err := fmt.Fprintf(w,
		"- **versionCode**: %d\n- **track**: %s\n- **status**: %s\n",
		r.VersionCode, r.Track, r.Status,
	); err != nil {
		return err
	}
	if showsUserFraction(r.Status, r.UserFraction) {
		if _, err := fmt.Fprintf(w, "- **userFraction**: %v\n", r.UserFraction); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "- **releaseName**: %s\n", r.ReleaseName)
	return err
}

// Run is the business function the kernel invokes. It validates the
// flag combination, resolves the package, builds an authenticated HTTP
// client from the active Account, then hands off to the orchestrator.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	// Mutual exclusion validation (ADR-0002 / DESIGN §3 / §9).
	if in.ReleaseNotes != "" && in.ReleaseNotesDir != "" {
		return nil, &usageError{msg: "--release-notes and --release-notes-dir are mutually exclusive"}
	}
	statusFlags := 0
	if in.Draft {
		statusFlags++
	}
	if in.Complete {
		statusFlags++
	}
	if in.StagedFractionSet {
		statusFlags++
	}
	if statusFlags > 1 {
		return nil, &usageError{msg: "--draft, --complete, and --staged are mutually exclusive"}
	}
	if in.StagedFractionSet && (in.StagedFraction <= 0 || in.StagedFraction > 1.0) {
		return nil, &usageError{msg: "--staged fraction must be in (0, 1]"}
	}
	if in.AABPath == "" {
		return nil, &usageError{msg: "missing AAB path: gplay releases upload <aab> ..."}
	}

	// Resolve package: --package flag → project pin.
	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return nil, &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}
	if in.Track == "" {
		return nil, &usageError{msg: "missing --track"}
	}

	// Classify APK vs AAB up front (before any HTTP and even on --dry-run)
	// so an unknown extension without --format is a usage error (exit 2),
	// never a late surprise after an Edit is opened.
	format, err := resolveFormat(in.AABPath, in.Format)
	if err != nil {
		return nil, err
	}

	// Dry-run skips auth AND the explicit-Edit pin entirely: nothing hits the
	// network, and the orchestrator's dry-run path never reuses a pinned Edit,
	// so a corrupt pin must not fail a preview. The pin is only resolved on the
	// live path below.
	var (
		httpClient     *http.Client
		explicitEditID string
	)
	if !in.DryRun {
		var err error
		// Reuse an open explicit Edit when one is pinned (`gplay edits begin`);
		// "" keeps the implicit per-upload Edit. A corrupt pin surfaces here.
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
		// UploadClient, not AuthedClient: the AAB transfer can be hundreds of
		// MB, so it is exempt from the 60s control-plane default (honors an
		// explicit --timeout). See kernel.RunContext.UploadClient.
		if httpClient, err = rc.UploadClient(); err != nil {
			return nil, err
		}
	}

	// Translate flag-shape Status to orchestrator Status.
	var status orchestrator.Status
	switch {
	case in.Draft:
		status = orchestrator.StatusDraft
	case in.Complete:
		status = orchestrator.StatusCompleted
	case in.StagedFractionSet:
		status = orchestrator.StatusInProgress
	default:
		status = orchestrator.StatusUnspecified
	}

	result, err := orchestrator.Upload(rc.Ctx, httpClient, orchestrator.Opts{
		Package:           pkg,
		Track:             in.Track,
		AABPath:           in.AABPath,
		Format:            format,
		MappingPath:       in.Mapping,
		Status:            status,
		UserFraction:      in.StagedFraction,
		ReleaseNotes:      in.ReleaseNotes,
		ReleaseNotesDir:   in.ReleaseNotesDir,
		KeepEditOnFailure: in.KeepEditOnFailure,
		ExplicitEditID:    explicitEditID,
		Confirm:           in.Confirm,
		DryRun:            in.DryRun,
	})
	if err != nil {
		// A tracks.update 404 means the target track does not exist yet;
		// attach the `gplay tracks create <name>` hint. Every other failure
		// (and the exit code) passes through untouched.
		return nil, trackhint.Classify(in.Track, err)
	}
	// DESIGN §8: a committed mutation prints one ✓ line on stderr (never on a
	// --dry-run). userFraction is shown only when status is inProgress, the
	// one status where a partial rollout fraction informs.
	if !in.DryRun {
		extra := ""
		if result.Status == "inProgress" {
			extra = ", userFraction " + output.Percent(result.UserFraction)
		}
		if result.MappingUploaded {
			extra += ", mapping uploaded"
		}
		rc.ConfirmMutation(explicitEditID, "uploaded versionCode %d to track %q (status %s%s)",
			result.VersionCode, result.Track, result.Status, extra)
	}
	return Payload{Result: result}, nil
}

// NewCommand returns the cobra command for `gplay releases upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag        string
		in                Input
		stagedFractionVar float64
	)
	cmd := &cobra.Command{
		Use:   "upload <artifact>",
		Short: "Upload an AAB or APK to a track on Google Play",
		Long: `Upload an AAB (or a legacy APK) and attach it to a release on the given track.

Performs the full Edit lifecycle in one call:
  edits.insert → bundles.upload (or apks.upload) → tracks.update → edits.commit

AAB vs APK is auto-detected by file extension (.aab / .apk); pass
--format apk|bundle to override when the extension is ambiguous. The rest
of the pipeline (track assignment, release notes, --mapping, draft-by-
default on production, --dry-run, --confirm) is identical for both.

Pass --mapping <mapping.txt> to upload the artifact's ProGuard/R8
deobfuscation file in the same Edit, so Play vitals can symbolicate
obfuscated crash stacks. To attach a mapping to an already-published
version, use gplay releases mappings upload instead.

Targeting production defaults to a draft release (ADR-0002) unless
--complete or --staged is supplied. Any string is accepted as --track
so closed-test tracks with custom names just work.

[experimental] APK upload: Google has required the AAB for new apps
since August 2021, so .apk uploads only serve existing apps still
distributed as APKs; if the app requires an App Bundle, Google's rejection
of the APK passes through verbatim.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.AABPath = args[0]
			// Detect explicit --staged so 0 is distinguished from "unset".
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "target track (internal, alpha, beta, production, or any closed-track name)")
	cmd.Flags().StringVar(&in.Format, "format", "", "artifact type: apk or bundle (overrides extension auto-detect)")
	cmd.Flags().StringVar(&in.Mapping, "mapping", "", "ProGuard/R8 deobfuscation file (mapping.txt) uploaded with the artifact so Play vitals can symbolicate obfuscated crash stacks")
	cmd.Flags().StringVar(&in.ReleaseNotes, "release-notes", "", "release notes text (applied to the app's default language)")
	cmd.Flags().StringVar(&in.ReleaseNotesDir, "release-notes-dir", "", "directory of <locale>.txt files (with optional default.txt fallback)")
	cmd.Flags().BoolVar(&in.Draft, "draft", false, "force the release status to draft")
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (1.0 user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f ≤ 1.0)")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required for production publishes (--complete / --staged on production)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	return cmd
}
