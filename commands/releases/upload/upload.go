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
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

type Input struct {
	Package string
	Track   string

	Artifacts          []string
	RetainVersionCodes []int

	Format            string
	Mapping           string
	ReleaseNotes      string
	ReleaseNotesSet   bool
	ReleaseNotesDir   string
	Draft             bool
	Complete          bool
	StagedFraction    float64
	StagedFractionSet bool
	UpdatePriority    int
	UpdatePrioritySet bool
	ReleaseName       string
	ReleaseNameSet    bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	Confirm           bool
	DryRun            bool
	SkipPreflight     bool
	DeviceTierConfig  string
	ExternallyHosted  string
}

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

func extFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".apk":
		return orchestrator.FormatAPK
	case ".aab":
		return orchestrator.FormatBundle
	}
	return ""
}

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

func hasBundle(artifacts []orchestrator.Artifact) bool {
	for _, a := range artifacts {
		if a.Format == orchestrator.FormatBundle {
			return true
		}
	}
	return false
}

func preflightKind(format string) artifact.Kind {
	if format == orchestrator.FormatAPK {
		return artifact.KindAPK
	}
	return artifact.KindBundle
}

type Payload struct {
	Result    *orchestrator.Result
	DryRun    bool
	Artifacts []orchestrator.Artifact
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

func (p Payload) multi() bool {
	return len(p.Result.UploadedVersionCodes) > 1 || len(p.Artifacts) > 1
}

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

func joinCodes(codes []int) string {
	s := make([]string, len(codes))
	for i, c := range codes {
		s[i] = strconv.Itoa(c)
	}
	return strings.Join(s, ", ")
}

func describeArtifacts(artifacts []orchestrator.Artifact) string {
	s := make([]string, len(artifacts))
	for i, a := range artifacts {
		s[i] = a.Path + " (" + a.Format + ")"
	}
	return strings.Join(s, ", ")
}

func showsUserFraction(status string, userFraction float64) bool {
	if status == tracks.StatusInProgress {
		return true
	}
	if status == tracks.StatusCompleted && userFraction > 0 {
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

type plannedArtifact struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

func renderJSON(w io.Writer, p Payload) error {
	r := p.Result
	if len(r.RawTrackResponse) > 0 {
		_, err := w.Write(r.RawTrackResponse)
		return err
	}
	shape := struct {
		DryRun bool `json:"dryRun,omitempty"`
		*orchestrator.Result
		Requires             *[]string         `json:"requires,omitempty"`
		UploadedVersionCodes []int             `json:"uploadedVersionCodes,omitempty"`
		RetainedVersionCodes []int             `json:"retainedVersionCodes,omitempty"`
		Artifacts            []plannedArtifact `json:"artifacts,omitempty"`
	}{DryRun: p.DryRun, Result: r, RetainedVersionCodes: r.RetainedVersionCodes}
	if p.DryRun {
		requires := output.NonNil(r.Requires)
		shape.Requires = &requires
	}
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

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	state, err := orchestrator.RequestedState(in.Draft, in.Complete, in.StagedFractionSet, in.StagedFraction)
	if err != nil {
		return nil, err
	}
	hosted := strings.TrimSpace(in.ExternallyHosted) != ""
	if in.ExternallyHosted != "" && !hosted {
		return nil, exit.Usagef("--externally-hosted must name a JSON file")
	}
	if (len(in.Artifacts) == 0 && !hosted) || slices.Contains(in.Artifacts, "") {
		return nil, &exit.UsageError{Msg: "missing AAB path: gplay releases upload <aab> ..."}
	}
	if hosted && len(in.Artifacts) == 0 && in.Format != "" {
		return nil, exit.Usagef("--format applies to uploaded artifacts, and --externally-hosted names none")
	}
	if total := len(in.Artifacts) + boolInt(hosted); in.Mapping != "" && total > 1 {
		return nil, exit.Usagef("--mapping applies to a single artifact (a mapping belongs to one versionCode), got %d: upload the other mappings with gplay releases mappings upload", total)
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	if in.Track == "" {
		return nil, exit.Usagef("missing --track: pass --track <name> (internal, alpha, beta, production, or any closed-track name)")
	}

	artifacts, err := resolveArtifacts(in.Artifacts, in.Format)
	if err != nil {
		return nil, err
	}
	if in.DeviceTierConfig != "" && !hasBundle(artifacts) {
		return nil, &exit.UsageError{Msg: "--device-tier-config applies to an App Bundle (.aab) only, not an APK"}
	}

	opts := orchestrator.Opts{
		Package:            pkg,
		Track:              in.Track,
		Artifacts:          artifacts,
		RetainVersionCodes: in.RetainVersionCodes,
		MappingPath:        in.Mapping,
		DeviceTierConfig:   in.DeviceTierConfig,
		State:              state,
		UpdatePriority:     updatePriority(in),
		ReleaseName:        in.ReleaseName,
		ReleaseNameSet:     in.ReleaseNameSet,
		ReleaseNotes:       in.ReleaseNotes,
		ReleaseNotesSet:    in.ReleaseNotesSet,
		ReleaseNotesDir:    in.ReleaseNotesDir,
		KeepEditOnFailure:  in.KeepEditOnFailure,
		Confirm:            in.Confirm,
		DryRun:             in.DryRun,
	}
	if hosted {
		opts.Artifacts = append(slices.Clip(artifacts), orchestrator.Artifact{Path: in.ExternallyHosted, Format: orchestrator.FormatExternallyHosted})
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	for _, a := range artifacts {
		if err := artifact.Verify(rc.Stderr, a.Path, artifact.Expect{
			Kinds:   []artifact.Kind{preflightKind(a.Format)},
			Package: pkg,
		}, artifact.Options{Skip: in.SkipPreflight, Report: in.DryRun}); err != nil {
			return nil, err
		}
	}

	if hosted {
		if _, err := orchestrator.LoadExternallyHosted(in.ExternallyHosted, pkg); err != nil {
			return nil, err
		}
	}

	var (
		httpClient     *http.Client
		explicitEditID string
	)
	if !in.DryRun {
		var err error
		if explicitEditID, err = rc.ExplicitEditID(pkg); err != nil {
			return nil, err
		}
		if httpClient, err = rc.UploadClient(); err != nil {
			return nil, err
		}
	}
	opts.ExplicitEditID = explicitEditID
	opts.Commit = in.Commit.For(rc, explicitEditID)

	result, err := orchestrator.Upload(rc.Ctx, httpClient, opts)
	if err != nil {
		return nil, trackhint.Classify(in.Track, err)
	}
	if !in.DryRun {
		extra := ""
		if result.Status == tracks.StatusInProgress {
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
	return Payload{Result: result, DryRun: in.DryRun, Artifacts: opts.Artifacts}, nil
}

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
one created. AAB only: with several artifacts it applies to every bundle.

[experimental] Pass --externally-hosted <file.json> to register an APK your
organisation serves from its own host instead of uploading it. Google
restricts this to Managed Play organisations publishing a private app (the
same population as gplay customapps); for any other app Google's refusal
passes through verbatim. The file is an ExternallyHostedApk resource (see
gplay schema ExternallyHostedApk). gplay only checks that it is a JSON object
and that its packageName, when present, is the package being released, both
before any Edit is opened; every other field is Google's to accept. The
versionCode Google returns joins the release like an uploaded artifact, so it
combines with positional artifacts and --retain-version-code, and replaces
the positional artifact when used alone. gplay neither hosts, uploads nor
signs the APK.`,
		Example: `  # Ship a build to internal testing, with its R8 mapping
  gplay releases upload app-release.aab --track internal --mapping mapping.txt

  # Ship a phone and a Wear bundle as one release, keeping versionCode 41 serving
  gplay releases upload phone.aab wear.aab --track beta --retain-version-code 41

  # Start a 10% staged rollout on production, release notes per locale
  gplay releases upload app-release.aab --track production --staged 0.1 \
    --release-notes-dir distribution/whatsnew --confirm

  # Preview the release payload without any HTTP call
  gplay releases upload app-release.aab --track production --dry-run --output json

  # Release an APK served from your own host (Managed Play private app)
  gplay releases upload --externally-hosted externally-hosted-apk.json --track internal`,
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("externally-hosted") {
				return nil
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.Artifacts = args
			in.StagedFractionSet = cmd.Flags().Changed("staged")
			in.StagedFraction = stagedFractionVar
			in.UpdatePrioritySet = cmd.Flags().Changed("update-priority")
			in.ReleaseNameSet = cmd.Flags().Changed("release-name")
			in.ReleaseNotesSet = cmd.Flags().Changed("release-notes")
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
	cmd.Flags().BoolVar(&in.Complete, "complete", false, "force the release status to completed (a full rollout, no user fraction)")
	cmd.Flags().Float64Var(&stagedFractionVar, "staged", 0, "start a staged rollout at this fraction (0 < f < 1; for 100% use --complete)")
	cmd.Flags().StringVar(&in.ReleaseName, "release-name", "", "name of the new release, as shown in the Play Console and matched by --release-name elsewhere (default: its versionCode)")
	cmd.Flags().IntVar(&in.UpdatePriority, "update-priority", 0, "in-app update priority of the release, 0 (default) to 5 (highest), read by the app through the Play in-app updates API")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "explicit confirmation required for production publishes (--complete / --staged on production)")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the release payload without any HTTP call")
	cmd.Flags().BoolVar(&in.SkipPreflight, "skip-preflight", false, "skip the local artifact check (container format and declared package name) and upload the file as-is")
	cmd.Flags().StringVar(&in.DeviceTierConfig, "device-tier-config", "", "device tier config id, or LATEST, applied to every uploaded bundle (AAB only)")
	cmd.Flags().IntSliceVar(&in.RetainVersionCodes, "retain-version-code", nil, "versionCode already uploaded that the new release keeps serving, passed to Google unchecked (repeatable)")
	cmd.Flags().StringVar(&in.ExternallyHosted, "externally-hosted", "", "ExternallyHostedApk JSON file registering an APK served from your own host, added to the release (Managed Play private apps only)")
	return kernel.ExperimentalFlag(cmd, "externally-hosted")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func updatePriority(in Input) *int {
	if !in.UpdatePrioritySet {
		return nil
	}
	p := in.UpdatePriority
	return &p
}
