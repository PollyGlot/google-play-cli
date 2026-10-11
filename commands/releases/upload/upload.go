// Package upload implements `gplay releases upload`: the CLI glue that
// resolves --package, parses the status flags (per ADR-0002), and hands
// an Opts payload to internal/releases/orchestrator. All the actual
// HTTP and Edit-lifecycle work lives in the orchestrator and its
// internal/play/* dependencies.
package upload

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/commands/releases/trackhint"
	"github.com/PollyGlot/google-play-cli/internal/artifact"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package string
	Track   string

	// Artifacts are the AAB/APK paths, one or more (#666): all uploaded into
	// one Edit and shipped as one release, or none at all.
	Artifacts []string
	// RetainVersionCodes is --retain-version-code (repeatable): versionCodes
	// the new release keeps serving next to the uploaded ones, passed to
	// Google unvalidated.
	RetainVersionCodes []int

	Format            string // "" | "apk" | "bundle": overrides extension auto-detect
	Mapping           string
	ReleaseNotes      string
	ReleaseNotesDir   string
	Draft             bool
	Complete          bool
	StagedFraction    float64
	StagedFractionSet bool
	// UpdatePriority is --update-priority (0..5); UpdatePrioritySet tells an
	// explicit 0 (lowest priority) apart from "not passed".
	UpdatePriority    int
	UpdatePrioritySet bool
	// ReleaseName is --release-name; ReleaseNameSet tells an explicit empty
	// value (a usage error) apart from "not passed" (versionCode default).
	ReleaseName       string
	ReleaseNameSet    bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	Confirm           bool
	DryRun            bool
	SkipPreflight     bool
	// DeviceTierConfig is --device-tier-config: a deviceTierConfigId or
	// LATEST, forwarded verbatim to edits.bundles.upload. A 1:1 mirror of the
	// Google parameter, so it is part of the frozen contract like the rest of
	// the command.
	DeviceTierConfig string
}

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
		if f := extFormat(path); f != "" {
			return f, nil
		}
		return "", &exit.UsageError{Msg: "cannot tell APK from AAB by extension: pass --format apk|bundle"}
	default:
		return "", &exit.UsageError{Msg: "--format must be apk or bundle"}
	}
}

// extFormat classifies path by its extension alone: FormatAPK for .apk,
// FormatBundle for .aab, "" when the extension says neither.
func extFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".apk":
		return orchestrator.FormatAPK
	case ".aab":
		return orchestrator.FormatBundle
	}
	return ""
}

// resolveArtifacts classifies every path, each on its own (a mixed AAB/APK
// set is left for Google to accept or refuse). With several artifacts one
// --format covers them all, so an extension naming the other container is a
// contradiction rather than an override, and is refused before anything is
// read. A single artifact keeps the historical override semantics: --format
// wins over its extension (Public contract).
func resolveArtifacts(paths []string, formatOverride string) ([]orchestrator.Artifact, error) {
	out := make([]orchestrator.Artifact, 0, len(paths))
	for _, p := range paths {
		f, err := resolveFormat(p, formatOverride)
		if err != nil {
			return nil, err
		}
		if len(paths) > 1 && formatOverride != "" {
			if ext := extFormat(p); ext != "" && ext != f {
				return nil, exit.Usagef("--format %s disagrees with the extension of %s: drop --format to detect each artifact by its extension", f, p)
			}
		}
		out = append(out, orchestrator.Artifact{Path: p, Format: f})
	}
	return out, nil
}

// hasBundle reports whether any artifact is an App Bundle.
func hasBundle(artifacts []orchestrator.Artifact) bool {
	for _, a := range artifacts {
		if a.Format != orchestrator.FormatAPK {
			return true
		}
	}
	return false
}

// preflightKind translates the resolved orchestrator format into the
// container the preflight must find. The two vocabularies already agree on
// the strings; the mapping is explicit so a future format value cannot
// silently become an unchecked upload.
func preflightKind(format string) artifact.Kind {
	if format == orchestrator.FormatAPK {
		return artifact.KindAPK
	}
	return artifact.KindBundle
}

// Payload satisfies output.Renderable for the resulting upload Result. DryRun
// marks the --dry-run preview, the path with no API body to pass through.
type Payload struct {
	Result *orchestrator.Result
	DryRun bool
	// Artifacts are the planned uploads, what a multi-artifact --dry-run
	// reports in place of the versionCodes it cannot know yet.
	Artifacts []orchestrator.Artifact
}

// Renderers returns the per-Format renderers. The JSON form is API
// pass-through (the raw tracks.update response, per ADR-0003); the
// table form is a human-shaped summary.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// multi reports whether the payload describes a release of several
// artifacts, the case the additive fields and lines exist for. One artifact
// keeps every historical shape byte-identical (Public contract, #666).
func (p Payload) multi() bool {
	return len(p.Result.UploadedVersionCodes) > 1 || len(p.Artifacts) > 1
}

// codeLines returns the label/value pairs both human formats lead with: the
// historical versionCode line for one artifact, every uploaded code for
// several (or, on a dry-run, the planned artifacts, whose codes only Google
// knows), then the retained codes when there are any.
func codeLines(p Payload) [][2]string {
	r := p.Result
	var lines [][2]string
	switch {
	case len(r.UploadedVersionCodes) > 1:
		lines = append(lines, [2]string{"versionCodes", joinCodes(r.UploadedVersionCodes)})
	case p.DryRun && len(p.Artifacts) > 1:
		lines = append(lines, [2]string{"artifacts", describeArtifacts(p.Artifacts)})
	default:
		lines = append(lines, [2]string{"versionCode", strconv.Itoa(r.VersionCode)})
	}
	if len(r.RetainedVersionCodes) > 0 {
		lines = append(lines, [2]string{"retained", joinCodes(r.RetainedVersionCodes)})
	}
	return lines
}

// joinCodes renders versionCodes as "142, 143".
func joinCodes(codes []int) string {
	s := make([]string, len(codes))
	for i, c := range codes {
		s[i] = strconv.Itoa(c)
	}
	return strings.Join(s, ", ")
}

// describeArtifacts renders planned uploads as "a.aab (bundle), b.apk (apk)".
func describeArtifacts(artifacts []orchestrator.Artifact) string {
	s := make([]string, len(artifacts))
	for i, a := range artifacts {
		s[i] = a.Path + " (" + a.Format + ")"
	}
	return strings.Join(s, ", ")
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

func renderTable(w io.Writer, p Payload) error {
	r := p.Result
	for _, l := range codeLines(p) {
		if _, err := fmt.Fprintf(w, "%-14s%s\n", l[0]+":", l[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w,
		"track:        %s\nstatus:       %s\n",
		r.Track, r.Status,
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

// plannedArtifact is one planned upload in the --dry-run JSON shape.
type plannedArtifact struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

func renderJSON(w io.Writer, p Payload) error {
	r := p.Result
	// API pass-through: emit the raw tracks.update body (ADR-0003), never
	// touched: the dryRun marker below lives only in gplay's own shape. The
	// body's release already lists every versionCode, retained ones included.
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	// Fallback to the gplay Result shape (the --dry-run preview, or a live
	// upload that somehow lost the raw), led under --dry-run by the dryRun
	// marker every other mutating command's preview carries. The trailing
	// fields are additive and only appear for several artifacts or retained
	// codes, so the single-artifact shape stays byte-identical (#666).
	shape := struct {
		DryRun bool `json:"dryRun,omitempty"`
		*orchestrator.Result
		UploadedVersionCodes []int             `json:"uploadedVersionCodes,omitempty"`
		RetainedVersionCodes []int             `json:"retainedVersionCodes,omitempty"`
		Artifacts            []plannedArtifact `json:"artifacts,omitempty"`
	}{DryRun: p.DryRun, Result: r, RetainedVersionCodes: r.RetainedVersionCodes}
	if p.multi() {
		shape.UploadedVersionCodes = r.UploadedVersionCodes
		if p.DryRun {
			for _, a := range p.Artifacts {
				shape.Artifacts = append(shape.Artifacts, plannedArtifact{Path: a.Path, Format: a.Format})
			}
		}
	}
	return output.WriteJSON(w, shape)
}

func renderMarkdown(w io.Writer, p Payload) error {
	r := p.Result
	for _, l := range codeLines(p) {
		if _, err := fmt.Fprintf(w, "- **%s**: %s\n", l[0], l[1]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w,
		"- **track**: %s\n- **status**: %s\n",
		r.Track, r.Status,
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
		return nil, &exit.UsageError{Msg: "--release-notes and --release-notes-dir are mutually exclusive"}
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
		return nil, &exit.UsageError{Msg: "--draft, --complete, and --staged are mutually exclusive"}
	}
	if in.StagedFractionSet && (in.StagedFraction <= 0 || in.StagedFraction > 1.0) {
		return nil, &exit.UsageError{Msg: "--staged fraction must be in (0, 1]"}
	}
	if in.UpdatePrioritySet && (in.UpdatePriority < 0 || in.UpdatePriority > orchestrator.MaxUpdatePriority) {
		return nil, exit.Usagef("--update-priority must be in 0..%d, got %d", orchestrator.MaxUpdatePriority, in.UpdatePriority)
	}
	if (in.ReleaseNameSet || in.ReleaseName != "") && strings.TrimSpace(in.ReleaseName) == "" {
		return nil, exit.Usagef("--release-name must not be empty or blank")
	}
	if len(in.Artifacts) == 0 || slices.Contains(in.Artifacts, "") {
		return nil, &exit.UsageError{Msg: "missing AAB path: gplay releases upload <aab> ..."}
	}
	// A mapping deobfuscates one versionCode: with several artifacts there is
	// no telling which one it belongs to, so it is refused before anything
	// else rather than attached to a guess.
	if in.Mapping != "" && len(in.Artifacts) > 1 {
		return nil, exit.Usagef("--mapping applies to a single artifact (a mapping belongs to one versionCode), got %d: upload the other mappings with gplay releases mappings upload", len(in.Artifacts))
	}

	// Resolve package: --package flag → project pin.
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (internal, alpha, beta, production, or any closed-track name)")
	}

	// Classify APK vs AAB up front (before any HTTP and even on --dry-run)
	// so an unknown extension without --format is a usage error (exit 2),
	// never a late surprise after an Edit is opened.
	artifacts, err := resolveArtifacts(in.Artifacts, in.Format)
	if err != nil {
		return nil, err
	}
	// A device tier config drives how Google splits an App Bundle; an APK is
	// already the deliverable, and edits.apks.upload has no such parameter.
	// It applies to every bundle of the set, and is refused when there is
	// none so it is never silently dropped.
	if in.DeviceTierConfig != "" && !hasBundle(artifacts) {
		return nil, &exit.UsageError{Msg: "--device-tier-config applies to an App Bundle (.aab) only, not an APK"}
	}

	// Artifact preflight (PRD #448): each artifact's container and declared
	// package are checked against what this invocation promised BEFORE any
	// Edit is opened and before the first upload byte leaves the machine, so
	// a wrong path fails in milliseconds instead of after minutes of
	// resumable transfer, and app A's release can never receive app B's
	// build. One mismatch fails the whole call (#666). Silent on success;
	// --skip-preflight is the parser-gap escape.
	for _, a := range artifacts {
		if err := artifact.Verify(rc.Stderr, a.Path, artifact.Expect{
			Kinds:   []artifact.Kind{preflightKind(a.Format)},
			Package: pkg,
		}, artifact.Options{Skip: in.SkipPreflight, Report: in.DryRun}); err != nil {
			return nil, err
		}
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
		Package:            pkg,
		Track:              in.Track,
		Artifacts:          artifacts,
		RetainVersionCodes: in.RetainVersionCodes,
		MappingPath:        in.Mapping,
		DeviceTierConfig:   in.DeviceTierConfig,
		Status:             status,
		UserFraction:       in.StagedFraction,
		UpdatePriority:     updatePriority(in),
		ReleaseName:        in.ReleaseName,
		ReleaseNotes:       in.ReleaseNotes,
		ReleaseNotesDir:    in.ReleaseNotesDir,
		KeepEditOnFailure:  in.KeepEditOnFailure,
		ExplicitEditID:     explicitEditID,
		Commit:             in.Commit.For(rc, explicitEditID),
		Confirm:            in.Confirm,
		DryRun:             in.DryRun,
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
		if len(result.RetainedVersionCodes) > 0 {
			extra += ", retained " + joinCodes(result.RetainedVersionCodes)
		}
		codes := "versionCode " + strconv.Itoa(result.VersionCode)
		if len(result.UploadedVersionCodes) > 1 {
			codes = "versionCodes " + joinCodes(result.UploadedVersionCodes)
		}
		rc.ConfirmMutation(explicitEditID, "uploaded %s to track %q (status %s%s)",
			codes, result.Track, result.Status, extra)
	}
	return Payload{Result: result, DryRun: in.DryRun, Artifacts: artifacts}, nil
}

// NewCommand returns the cobra command for `gplay releases upload`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag        string
		in                Input
		stagedFractionVar float64
	)
	cmd := &cobra.Command{
		Use:   "upload <artifact>...",
		Short: "Upload an AAB or APK to a track on Google Play",
		Long: `Upload an AAB (or a legacy APK) and attach it to a release on the given track.

Performs the full Edit lifecycle in one call:
  edits.insert → bundles.upload (or apks.upload) per artifact → tracks.update → edits.commit

Pass several artifacts to ship one release made of all of them, such as
per-ABI APKs or a phone and a Wear bundle: every artifact is uploaded in
the same Edit and the release lists every versionCode, in one track update
and one commit. It is all or nothing: if any check, upload or the track
update fails, the Edit is discarded and nothing ships (with an Edit opened
by gplay edits begin, nothing is committed). With several artifacts the
release is named after the highest uploaded versionCode.

Pass --retain-version-code N, repeatable, to keep a versionCode already
uploaded serving in the new release next to the uploaded ones. gplay does
not check it: an unknown or inactive code fails with Google's own error.

AAB vs APK is auto-detected by file extension (.aab / .apk), per artifact;
pass --format apk|bundle to override when the extension is ambiguous. With
several artifacts --format applies to all of them and is refused when an
artifact's extension names the other format. The rest of the pipeline
(track assignment, release notes, --mapping, draft-by-default on
production, --dry-run, --confirm) is identical for both.

Before any byte is uploaded, each artifact is checked locally: its container
must really be the format this call promised, and the package name its
manifest declares must match the package being released. A mismatch fails
offline in milliseconds, naming what was expected and what was found, so no
Edit is opened and no upload session is reserved for a doomed artifact. Pass
--skip-preflight to upload the file as-is.

Pass --mapping <mapping.txt> to upload the artifact's ProGuard/R8
deobfuscation file in the same Edit, so Play vitals can symbolicate
obfuscated crash stacks. A mapping belongs to one versionCode, so
--mapping takes a single artifact. To attach a mapping to an already-
published version, or to each artifact of a multi-artifact release, use
gplay releases mappings upload instead.

The new release is named after its versionCode. Pass --release-name to
name it yourself (a versionName such as 2.4.1, say): it is the name the
Play Console shows and the one --release-name matches on releases promote,
rollout, complete, halt and resume.

Targeting production defaults to a draft release, which reaches no user,
unless --complete or --staged is supplied (both require --confirm there).
Any string is accepted as --track so Closed tracks with custom names just
work. See https://gplay.sh/docs/concepts/tracks-and-releases/

[experimental] APK upload: Google has required the AAB for new apps
since August 2021, so .apk uploads only serve existing apps still
distributed as APKs; if the app requires an App Bundle, Google's rejection
of the APK passes through verbatim.

Pass --device-tier-config to attach a device tier config to the
uploaded bundle, so Google generates its deliverables for the device tiers
it defines. Pass an id from gplay device-tiers list, or LATEST for the last
one created. AAB only: with several artifacts it applies to every bundle.`,
		Example: `  # Ship a build to internal testing, with its R8 mapping
  gplay releases upload app-release.aab --track internal --mapping mapping.txt

  # Ship a phone and a Wear bundle as one release, keeping versionCode 41 serving
  gplay releases upload phone.aab wear.aab --track beta --retain-version-code 41

  # Start a 10% staged rollout on production, release notes per locale
  gplay releases upload app-release.aab --track production --staged 0.1 \
    --release-notes-dir distribution/whatsnew --confirm

  # Preview the release payload without any HTTP call
  gplay releases upload app-release.aab --track production --dry-run --output json`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.Artifacts = args
			// Detect explicit --staged so 0 is distinguished from "unset".
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			in.UpdatePrioritySet = cmd.Flags().Changed("update-priority")
			in.ReleaseNameSet = cmd.Flags().Changed("release-name")
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Track, "track", "", "target track (internal, alpha, beta, production, or any closed-track name) (required)")
	cmd.Flags().StringVar(&in.Format, "format", "", "artifact type: apk or bundle (overrides extension auto-detect)")
	cmd.Flags().StringVar(&in.Mapping, "mapping", "", "ProGuard/R8 deobfuscation file (mapping.txt) uploaded with the artifact so Play vitals can symbolicate obfuscated crash stacks")
	cmd.Flags().StringVar(&in.ReleaseNotes, "release-notes", "", "release notes text (applied to the app's default language)")
	cmd.Flags().StringVar(&in.ReleaseNotesDir, "release-notes-dir", "", "directory of <locale>.txt files in BCP 47, e.g. en-US.txt (with optional default.txt fallback)")
	cmd.Flags().BoolVar(&in.Draft, "draft", false, "force the release status to draft")
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (1.0 user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f ≤ 1.0)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "name of the new release, as shown in the Play Console and matched by --release-name elsewhere (default: its versionCode)")
	cmd.Flags().IntVar(&in.UpdatePriority, "update-priority", 0, "in-app update priority of the release, 0 (default) to 5 (highest), read by the app through the Play in-app updates API")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required for production publishes (--complete / --staged on production)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	cmd.Flags().BoolVar(&in.SkipPreflight, "skip-preflight", false, "skip the local artifact check (container format and declared package name) and upload the file as-is")
	cmd.Flags().StringVar(&in.DeviceTierConfig, "device-tier-config", "", "device tier config id, or LATEST, applied to every uploaded bundle (AAB only)")
	cmd.Flags().IntSliceVar(&in.RetainVersionCodes, "retain-version-code", nil, "versionCode already uploaded that the new release keeps serving, passed to Google unchecked (repeatable)")
	return cmd
}

// updatePriority maps the flag pair to the orchestrator's optional priority:
// nil when --update-priority was not passed, so the field stays off the wire.
func updatePriority(in Input) *int {
	if !in.UpdatePrioritySet {
		return nil
	}
	p := in.UpdatePriority
	return &p
}
