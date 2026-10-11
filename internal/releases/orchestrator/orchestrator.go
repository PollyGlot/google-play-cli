// Package orchestrator owns the choreography of `gplay releases upload`:
// open an Edit, upload the artifacts, attach the resulting versionCodes to
// one release on the target track, and commit the Edit. Safe-default rules
// from ADR-0002, release notes loading, and the auto-discard error path land in
// subsequent TDD blocks; the tracer bullet here is the minimum
// vertical slice that exercises every collaborating module.
package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

// confirmRequired builds the refusal for a production publish that needs an
// explicit --confirm to proceed (ADR-0002). It is an *exit.SafetyFlagError, so
// it exits 3 ("safety flag required", docs/DESIGN.md §9) and names the
// missing flag in the --output json error envelope's requires[] (ADR-0017 /
// ADR-0023). It is deliberately NOT exit 2: the invocation is well-formed, and
// the refusal is deterministically resolvable by re-running with --confirm.
func confirmRequired(status string) error {
	return exit.SafetyFlag("confirm", "production publish (status=%s) requires --confirm to prevent accidental rollouts", status)
}

// InvalidOptsError signals a caller-side validation failure: a Status /
// UserFraction combination Google Play would reject, or any other
// orchestrator-level precondition. It maps to exit code 2 so the CLI
// taxonomy stays consistent with the command-layer usage errors.
type InvalidOptsError struct {
	Message string
}

func (e *InvalidOptsError) Error() string { return e.Message }
func (e *InvalidOptsError) ExitCode() int { return 2 }

// validateStatusValue rejects any Status value outside the four
// declared constants. Used by both validateOpts (upload) and
// validatePromoteOpts (promote) so an out-of-band Status fails fast
// with *InvalidOptsError (exit 2): without this, statusPayload and
// resolvedStatus silently fall through to status="" and ship an
// invalid wire payload.
func validateStatusValue(status Status) error {
	switch status {
	case StatusUnspecified, StatusDraft, StatusCompleted, StatusInProgress:
		return nil
	}
	return &InvalidOptsError{
		Message: "Status value is not one of StatusUnspecified, StatusDraft, StatusCompleted, StatusInProgress",
	}
}

// MaxUpdatePriority is the highest inAppUpdatePriority Google Play accepts;
// the range is 0..MaxUpdatePriority.
const MaxUpdatePriority = 5

// validateUpdatePriority rejects a priority outside 0..5 before any HTTP, so
// a typo never costs an Edit. Shared by upload and promote.
func validateUpdatePriority(p *int) error {
	if p != nil && (*p < 0 || *p > MaxUpdatePriority) {
		return &InvalidOptsError{Message: "UpdatePriority must be in 0..5"}
	}
	return nil
}

// resolvedStatus reports the wire-format release status (draft /
// completed / inProgress) a given Status + Track would produce after
// applying the ADR-0002 safe-default rule. Shared by both the upload
// and promote orchestrators so the rule is defined exactly once.
func resolvedStatus(track string, status Status) string {
	switch status {
	case StatusUnspecified:
		if track == TrackProduction {
			return "draft"
		}
		return "completed"
	case StatusDraft:
		return "draft"
	case StatusCompleted:
		return "completed"
	case StatusInProgress:
		return "inProgress"
	}
	return ""
}

// requiresConfirm reports whether a release operation would publish to
// real users on production. Draft operations (explicit or safe-default)
// do not need confirmation. Non-production tracks affect only testers.
// Shared by upload and promote.
func requiresConfirm(track string, status Status) bool {
	if track != TrackProduction {
		return false
	}
	s := resolvedStatus(track, status)
	return s == "completed" || s == "inProgress"
}

// statusPayload resolves the (status, userFraction) pair the wire
// payload must carry, given a caller-supplied Status + UserFraction and
// the target Track. Applies the ADR-0002 safe-default rule when Status
// is Unspecified (production → draft, others → completed/1.0). Shared
// by buildRelease (upload) and buildPromoteRelease (promote).
func statusPayload(track string, status Status, userFraction float64) (string, float64) {
	switch status {
	case StatusUnspecified:
		if track == TrackProduction {
			return "draft", 0
		}
		return "completed", 1.0
	case StatusDraft:
		return "draft", 0
	case StatusCompleted:
		return "completed", 1.0
	case StatusInProgress:
		return "inProgress", userFraction
	}
	return "", 0
}

// Status drives the release status payload sent on the target track.
// StatusUnspecified means "apply the safe-default rule" (ADR-0002) and
// will be honored in Block 1 cycles 2-4. For the tracer bullet only
// StatusUnspecified is exercised and currently coerces to Completed.
type Status int

const (
	StatusUnspecified Status = iota
	StatusDraft
	StatusCompleted
	StatusInProgress
)

// Opts is the input contract for Upload. The orchestrator owns the
// ADR-0002 safe-default rule (production → draft when Status is
// Unspecified) and the --confirm guard for production publishes.
type Opts struct {
	Package string
	Track   string
	// Artifacts are the files uploaded into the Edit, in order: one or more
	// AABs or APKs (#666). Every one is uploaded before the single
	// tracks.update, and the release carries all of their versionCodes, so a
	// release made of several artifacts (per-ABI APKs, a phone and a Wear
	// bundle) ships in one commit or not at all.
	Artifacts []Artifact
	// RetainVersionCodes are versionCodes already uploaded to the app that the
	// new release keeps serving, appended after the uploaded ones. They are
	// passed to Google unvalidated: an unknown or inactive code fails with
	// Google's own error (ADR-0003), and no extra read call is made.
	RetainVersionCodes []int

	Status       Status
	UserFraction float64
	// UpdatePriority, when non-nil, sets the release's inAppUpdatePriority
	// (0..5). nil leaves the field off the wire (#664).
	UpdatePriority *int
	// ReleaseName, when non-empty, names the new release (#665); empty keeps
	// the versionCode as its name. Whitespace-only is refused.
	ReleaseName       string
	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool

	// ExplicitEditID, when set, reuses an already-open explicit Edit
	// (`gplay edits begin`) instead of opening/committing a per-upload Edit:
	// the upload mutates the pinned Edit and leaves it open for the user to
	// `gplay edits commit` (docs/DESIGN.md §4). Empty is the implicit default.
	ExplicitEditID string

	// Commit carries the opt-in edits.commit parameters; the zero value keeps
	// Google's default. Unused with ExplicitEditID (`gplay edits commit` commits).
	Commit edits.CommitOptions

	// MappingPath, when set, uploads a ProGuard/R8 deobfuscation file (a
	// Mapping) alongside the AAB in the SAME Edit, keyed by the versionCode
	// the bundle upload returns. Empty means no mapping is uploaded. This
	// is the `releases upload --mapping` common case (#250); symbolicating
	// obfuscated crash stacks in Play vitals depends on it.
	MappingPath string

	// DeviceTierConfig, when set, is forwarded as the deviceTierConfigId
	// of edits.bundles.upload: the device tier config (an id, or "LATEST")
	// Google generates the bundle's deliverables with. AAB only: the command
	// layer refuses it for an APK before anything is opened.
	DeviceTierConfig string

	// Confirm gates production-impacting writes. Required when Track is
	// "production" AND Status would publish to real users (Completed or
	// InProgress). Draft / safe-default production uploads do not need
	// it (the release is not visible until promoted).
	Confirm bool

	// DryRun validates inputs and computes what would be sent without
	// performing any HTTP calls. No Edit is opened, no AAB is uploaded,
	// no track is updated. The returned Result describes the planned
	// payload with a synthetic versionCode=0.
	DryRun bool
}

// Result is what Upload returns on success. RawTrackResponse carries
// the raw tracks.update JSON for --output json pass-through (ADR-0003).
type Result struct {
	// VersionCode is the highest uploaded versionCode: the only one for a
	// single artifact, the one a multi-artifact release is named after.
	VersionCode  int     `json:"versionCode"`
	Track        string  `json:"track"`
	ReleaseName  string  `json:"releaseName"`
	Status       string  `json:"status"`
	UserFraction float64 `json:"userFraction,omitempty"`
	// InAppUpdatePriority is the priority the release carries (or, on
	// --dry-run, would carry); nil when none is set.
	InAppUpdatePriority *int            `json:"inAppUpdatePriority,omitempty"`
	DefaultLanguage     string          `json:"defaultLanguage,omitempty"`
	Locales             []string        `json:"locales,omitempty"`
	RawTrackResponse    json.RawMessage `json:"-"`

	// MappingUploaded reports whether a ProGuard/R8 mapping was uploaded
	// alongside the AAB (the --mapping path, #250). The ✓ confirmation
	// line surfaces it; the JSON pass-through stays the tracks.update body.
	MappingUploaded bool `json:"-"`

	// UploadedVersionCodes lists the versionCode of every uploaded artifact,
	// in argument order (nil on --dry-run: they are only known once Google
	// has read the files). RetainedVersionCodes echoes Opts.RetainVersionCodes.
	// Both stay out of this struct's JSON: whether they join the
	// gplay-authored shape is the command's Public-contract call, which keeps
	// the single-artifact shape byte-identical (#666).
	UploadedVersionCodes []int `json:"-"`
	RetainedVersionCodes []int `json:"-"`
}

// Artifact is one file Upload sends into the Edit. Format selects its upload
// endpoint: FormatBundle (or empty) rides edits.bundles.upload, FormatAPK
// rides edits.apks.upload (ADR-0036), and FormatExternallyHosted names a JSON
// ExternallyHostedApk resource registered through
// edits.apks.addexternallyhosted (#546). The command layer resolves it per
// file from the extension or an explicit --format; the versionCode every
// endpoint returns drives the rest of the pipeline unchanged.
type Artifact struct {
	Path   string
	Format string
}

// Upload performs the upload flow inside a single Edit:
// edits.insert → (optional details.get for release notes) →
// bundles.upload per artifact → notes.Load → tracks.update → edits.commit.
// Release notes are fetched only when --release-notes or
// --release-notes-dir is supplied; otherwise the extra
// details.get round-trip is skipped.
//
// Production publishes (track == "production" with status that would
// reach real users) require opts.Confirm = true; otherwise Upload
// returns an *exit.SafetyFlagError naming --confirm (exit 3).
//
// opts.DryRun runs the input validation and previews what would be
// sent, without any HTTP. The returned Result has VersionCode=0 and
// ReleaseName="(dry-run)", or opts.ReleaseName when one is set.
func Upload(ctx context.Context, hc *http.Client, opts Opts) (*Result, error) {
	// Caller-side validation runs first so dry-run callers still see
	// invalid-opts errors and live callers don't open an Edit only to
	// discover their flag combination was bad.
	if err := validateOpts(opts); err != nil {
		return nil, err
	}
	// Externally hosted resources are read and checked here, before the
	// dry-run branch and before edits.insert: a malformed file or another
	// app's package never costs an Edit (#546).
	hosted, err := loadHosted(opts)
	if err != nil {
		return nil, err
	}

	// Dry-run is evaluated BEFORE the --confirm guard so users can
	// preview a production publish (e.g. to inspect what --staged 0.05
	// would emit) without having to satisfy the confirm guardrail. The
	// preview path performs no HTTP and has no side effects.
	if opts.DryRun {
		return dryRunResult(opts)
	}

	if requiresConfirm(opts.Track, opts.Status) && !opts.Confirm {
		return nil, confirmRequired(resolvedStatus(opts.Track, opts.Status))
	}

	result := &Result{Track: opts.Track}

	err = edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		var localized []tracks.LocalizedText
		if opts.ReleaseNotes != "" || opts.ReleaseNotesDir != "" {
			// details.get is an extra round-trip; only do it when the
			// notes loader actually needs DefaultLanguage: i.e. when
			// the user supplied --release-notes text, or when the
			// directory contains a default.txt fallback. A directory
			// with only explicit per-locale files works without it.
			needLang := opts.ReleaseNotes != "" || hasDefaultTxt(opts.ReleaseNotesDir)
			loadOpts := notes.Opts{
				Text: opts.ReleaseNotes,
				Dir:  opts.ReleaseNotesDir,
			}
			if needLang {
				lang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
				if err != nil {
					return err
				}
				result.DefaultLanguage = lang
				loadOpts.DefaultLanguage = lang
			}
			loaded, err := notes.Load(loadOpts)
			if err != nil {
				return err
			}
			localized = make([]tracks.LocalizedText, 0, len(loaded))
			for _, n := range loaded {
				localized = append(localized, tracks.LocalizedText{Language: n.Locale, Text: n.Text})
				result.Locales = append(result.Locales, n.Locale)
			}
		}

		// Every artifact is uploaded into the same Edit before the single
		// tracks.update: the first failure aborts the closure, so an implicit
		// Edit is discarded and an explicit one is left uncommitted, and no
		// partial release ever ships (#666).
		codes := make([]int, 0, len(opts.Artifacts))
		for i, a := range opts.Artifacts {
			code, err := uploadArtifact(ctx, hc, opts, editID, a, hosted[i])
			if err != nil {
				return err
			}
			codes = append(codes, code)
		}
		result.VersionCode = highest(codes)
		result.UploadedVersionCodes = codes
		result.RetainedVersionCodes = opts.RetainVersionCodes

		// Upload the ProGuard/R8 mapping in the SAME edit once the
		// versionCode is known. A failure here aborts the closure: in IMPLICIT
		// mode WithEdit auto-discards so we never commit a release whose mapping
		// silently failed to attach; in EXPLICIT mode the pinned Edit is left
		// open for the user to retry or discard. Either way the release is not
		// committed without its mapping: the whole point is readable crash
		// stacks (#250). validateOpts guarantees a single artifact here.
		if opts.MappingPath != "" {
			if _, err := mappings.Upload(ctx, hc, opts.Package, editID, codes[0], mappings.TypeProguard, opts.MappingPath); err != nil {
				return err
			}
			result.MappingUploaded = true
		}

		release := buildRelease(codes, opts)
		release.ReleaseNotes = localized
		_, raw, err := tracks.Update(ctx, hc, opts.Package, editID, opts.Track, release)
		if err != nil {
			return err
		}
		result.ReleaseName = release.Name
		result.Status = release.Status
		result.UserFraction = release.UserFraction
		result.InAppUpdatePriority = release.InAppUpdatePriority
		result.RawTrackResponse = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// uploadArtifact sends one artifact into the Edit and returns the versionCode
// Google assigned it. An APK rides edits.apks.upload, an AAB (the default)
// rides edits.bundles.upload with the device tier config, if any: a tier
// config only means something to a bundle, so a mixed set applies it to the
// bundles alone (ADR-0036). An externally hosted APK sends no bytes: its
// resource, already loaded and checked by loadHosted, rides
// edits.apks.addexternallyhosted and joins the release like any upload (#546).
func uploadArtifact(ctx context.Context, hc *http.Client, opts Opts, editID string, a Artifact, hosted json.RawMessage) (int, error) {
	switch a.Format {
	case FormatAPK:
		return apks.Upload(ctx, hc, opts.Package, editID, a.Path)
	case FormatExternallyHosted:
		code, _, err := apks.AddExternallyHosted(ctx, hc, opts.Package, editID, hosted)
		return code, err
	}
	return bundles.UploadWith(ctx, hc, opts.Package, editID, a.Path,
		bundles.Options{DeviceTierConfigID: opts.DeviceTierConfig})
}

// highest returns the largest of codes, 0 for none.
func highest(codes []int) int {
	top := 0
	for _, c := range codes {
		top = max(top, c)
	}
	return top
}

// validateOpts enforces the orchestrator-level preconditions every
// caller (CLI, future MCP server, library user) must satisfy. Centralizing
// these checks here keeps exit semantics stable across dry-run and live
// paths: every misuse maps to *InvalidOptsError (exit 2), never to the
// dry-run validation code (20) and never to a deeper-layer surprise.
// Checks that depend on Track and Status interplay (safe-default rule)
// belong in buildRelease.
func validateOpts(opts Opts) error {
	if err := validateStatusValue(opts.Status); err != nil {
		return err
	}
	if len(opts.Artifacts) == 0 {
		return &InvalidOptsError{Message: "Artifacts must name at least one file to upload"}
	}
	// A mapping deobfuscates one versionCode: with several artifacts there is
	// no telling which one it belongs to, so it is refused rather than
	// guessed (`releases mappings upload` attaches the others).
	if opts.MappingPath != "" && len(opts.Artifacts) > 1 {
		return &InvalidOptsError{Message: "MappingPath applies to a single artifact, got " + strconv.Itoa(len(opts.Artifacts))}
	}
	if err := validateUpdatePriority(opts.UpdatePriority); err != nil {
		return err
	}
	if opts.ReleaseName != "" && strings.TrimSpace(opts.ReleaseName) == "" {
		return &InvalidOptsError{Message: "ReleaseName must not be blank"}
	}
	if opts.ReleaseNotes != "" && opts.ReleaseNotesDir != "" {
		return &InvalidOptsError{
			Message: "ReleaseNotes and ReleaseNotesDir are mutually exclusive: pick one",
		}
	}
	if opts.Status == StatusInProgress {
		if opts.UserFraction <= 0 || opts.UserFraction > 1.0 {
			return &InvalidOptsError{
				Message: "UserFraction must be in (0, 1] when Status is InProgress",
			}
		}
	}
	// Locale names in a release-notes dir are FILE NAMES, so they are knowable
	// offline: check them here, where validateOpts already runs before the
	// artifact upload and before edits.insert. Deferring to notes.Load (inside
	// WithEdit) meant an `en_US.txt` typo burnt an Edit before failing (#452).
	if err := notes.ValidateDirLocales(opts.ReleaseNotesDir); err != nil {
		return err
	}
	return nil
}

// hasDefaultTxt reports whether a release-notes directory contains a
// default.txt fallback. The orchestrator uses it to decide whether the
// extra edits.details.get round-trip is needed.
func hasDefaultTxt(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "default.txt")); err == nil {
		return true
	}
	return false
}

// buildRelease translates Opts + the uploaded versionCodes into a
// tracks.Release payload: the uploaded codes in argument order, then the
// retained ones, unvalidated. The default name is the highest uploaded code
// (the only one for a single artifact). The ADR-0002 safe-default rule and
// the status→userFraction mapping live in statusPayload, shared with
// buildPromoteRelease so the wire shape stays identical across upload
// and promote.
func buildRelease(uploaded []int, opts Opts) tracks.Release {
	statusStr, userFraction := statusPayload(opts.Track, opts.Status, opts.UserFraction)
	name := strconv.Itoa(highest(uploaded))
	if opts.ReleaseName != "" {
		name = opts.ReleaseName
	}
	codes := make([]string, 0, len(uploaded)+len(opts.RetainVersionCodes))
	for _, c := range uploaded {
		codes = append(codes, strconv.Itoa(c))
	}
	for _, c := range opts.RetainVersionCodes {
		codes = append(codes, strconv.Itoa(c))
	}
	return tracks.Release{
		Name:                name,
		Status:              statusStr,
		UserFraction:        userFraction,
		VersionCodes:        codes,
		InAppUpdatePriority: opts.UpdatePriority,
	}
}

// TrackProduction is the canonical name of Google Play's production
// track. The safe-default rule in buildRelease tests against this
// constant rather than a string literal so the comparison is grep-able.
const TrackProduction = "production"

// Artifact format values for Artifact.Format. FormatBundle is the empty
// default (edits.bundles.upload); FormatAPK routes the legacy path
// (edits.apks.upload, ADR-0036).
const (
	FormatBundle = "bundle"
	FormatAPK    = "apk"
	// FormatExternallyHosted marks an Artifact whose Path is a JSON
	// ExternallyHostedApk resource, not a binary (#546).
	FormatExternallyHosted = "externallyHosted"
)

// HostedError is the local refusal of an externally hosted APK resource: the
// file cannot be read, is not a JSON object, or declares another app's
// package (or a non-string one). It exits 20 (client-side validation, docs/DESIGN.md §9), like the
// artifact preflight that guards the uploaded files of the same command.
type HostedError struct {
	Path string
	Msg  string
}

func (e *HostedError) Error() string { return "externally hosted APK " + e.Path + ": " + e.Msg }
func (e *HostedError) ExitCode() int { return 20 }

// LoadExternallyHosted reads the ExternallyHostedApk resource at path and
// applies the only checks gplay owns (#546): the file is a JSON object, and
// its packageName, when present, is pkg. Every other field (URL, hashes,
// certificates, versionCode) is Google's to accept or refuse verbatim. The
// bytes are returned untouched, ready to send.
func LoadExternallyHosted(path, pkg string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &HostedError{Path: path, Msg: err.Error()}
	}
	if !json.Valid(data) {
		return nil, &HostedError{Path: path, Msg: "not valid JSON (an ExternallyHostedApk resource; see gplay schema ExternallyHostedApk)"}
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil || probe == nil {
		return nil, &HostedError{Path: path, Msg: "not a JSON object (an ExternallyHostedApk resource; see gplay schema ExternallyHostedApk)"}
	}
	if raw, ok := probe["packageName"]; ok {
		var declared string
		if err := json.Unmarshal(raw, &declared); err != nil {
			return nil, &HostedError{Path: path, Msg: "packageName is not a string"}
		}
		if declared != pkg {
			return nil, &HostedError{Path: path, Msg: "packageName is " + strconv.Quote(declared) + " but the package being released is " + strconv.Quote(pkg)}
		}
	}
	return json.RawMessage(data), nil
}

// loadHosted loads every externally hosted artifact of opts, indexed like
// opts.Artifacts (nil for an uploaded file), so the first bad resource fails
// the call before anything is sent.
func loadHosted(opts Opts) ([]json.RawMessage, error) {
	hosted := make([]json.RawMessage, len(opts.Artifacts))
	for i, a := range opts.Artifacts {
		if a.Format != FormatExternallyHosted {
			continue
		}
		raw, err := LoadExternallyHosted(a.Path, opts.Package)
		if err != nil {
			return nil, err
		}
		hosted[i] = raw
	}
	return hosted, nil
}

// dryRunDefaultLangPlaceholder satisfies notes.Load's DefaultLanguage
// requirement during a dry-run so the loader's structural validation
// (mutual exclusion, file-size cap, missing dir) still runs. The real
// DefaultLanguage is resolved at upload time via edits.details.get.
const dryRunDefaultLangPlaceholder = "(default-language-at-upload-time)"

// dryRunResult validates the inputs Upload would consume without any
// HTTP and returns a preview Result describing the planned payload.
// Caller-side mutual-exclusion / fraction-range checks already ran in
// validateOpts; this function only adds the dry-run-specific FS checks
// (AAB reachable, notes dir walkable) and the notes loader's own
// structural validation via a placeholder DefaultLanguage.
func dryRunResult(opts Opts) (*Result, error) {
	for _, a := range opts.Artifacts {
		st, err := os.Stat(a.Path)
		if err != nil {
			return nil, &dryRunError{msg: "artifact not accessible: " + err.Error()}
		}
		// Parity with the live path (bundles.Upload): a directory passes
		// Stat but cannot be streamed, so reject non-regular files here too
		//: otherwise --dry-run would green-light what the live upload rejects.
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "artifact is not a regular file: " + a.Path}
		}
	}

	if opts.MappingPath != "" {
		st, err := os.Stat(opts.MappingPath)
		if err != nil {
			return nil, &dryRunError{msg: "mapping not accessible: " + err.Error()}
		}
		// Parity with the live path (mappings.Upload): a directory passes
		// Stat but cannot be streamed, so reject non-regular files here too
		//: otherwise --dry-run would green-light what the live upload rejects.
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "mapping is not a regular file: " + opts.MappingPath}
		}
	}

	var previewLocales []string
	if opts.ReleaseNotesDir != "" {
		st, err := os.Stat(opts.ReleaseNotesDir)
		if err != nil {
			return nil, &dryRunError{msg: "release-notes-dir not accessible: " + err.Error()}
		}
		if !st.IsDir() {
			return nil, &dryRunError{msg: "release-notes-dir is not a directory: " + opts.ReleaseNotesDir}
		}
		// Run the actual loader so the file-size cap, per-locale walk,
		// and default.txt rules are all exercised. The placeholder
		// DefaultLanguage satisfies notes.Load's API contract; the live
		// upload resolves the real value via details.get.
		loaded, err := notes.Load(notes.Opts{
			Dir:             opts.ReleaseNotesDir,
			DefaultLanguage: dryRunDefaultLangPlaceholder,
		})
		if err != nil {
			return nil, &dryRunError{msg: err.Error()}
		}
		for _, n := range loaded {
			if n.Locale == dryRunDefaultLangPlaceholder {
				previewLocales = append(previewLocales, "(default-language)")
			} else {
				previewLocales = append(previewLocales, n.Locale)
			}
		}
	}

	// Compute the would-be release shape via the same builder the live
	// flow uses, with a synthetic versionCode 0 since the real ones are
	// only known after the uploads. The retained codes are known already.
	release := buildRelease([]int{0}, opts)
	return &Result{
		Track:                opts.Track,
		VersionCode:          0,
		Status:               release.Status,
		UserFraction:         release.UserFraction,
		InAppUpdatePriority:  release.InAppUpdatePriority,
		ReleaseName:          dryRunReleaseName(opts),
		Locales:              previewLocales,
		RetainedVersionCodes: opts.RetainVersionCodes,
	}, nil
}

// dryRunError is the validation-failure error for dry-run mode. It
// maps to exit 20 (client-side validation) per docs/DESIGN.md §9.
type dryRunError struct{ msg string }

func (e *dryRunError) Error() string { return "dry-run: " + e.msg }
func (e *dryRunError) ExitCode() int { return 20 }

// dryRunReleaseName is the name a --dry-run preview shows: the explicit name
// when one is set (known before any upload), otherwise the "(dry-run)"
// placeholder, since the versionCode default only exists after the upload.
func dryRunReleaseName(opts Opts) string {
	if opts.ReleaseName != "" {
		return opts.ReleaseName
	}
	return "(dry-run)"
}
