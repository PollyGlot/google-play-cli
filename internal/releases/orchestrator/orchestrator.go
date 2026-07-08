// Package orchestrator owns the choreography of `gplay releases upload`:
// open an Edit, upload the AAB, attach the resulting versionCode to the
// target track, and commit the Edit. Safe-default rules from ADR-0002,
// release notes loading, and the auto-discard error path land in
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

	"github.com/PollyGlot/google-play-cli/internal/play/apks"
	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/mappings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

// ConfirmRequiredError signals a production publish that needs an
// explicit --confirm to proceed. It maps to exit code 2 (CLI misuse).
type ConfirmRequiredError struct {
	Track  string
	Status string
}

func (e *ConfirmRequiredError) Error() string {
	return "production publish (status=" + e.Status + ") requires --confirm to prevent accidental rollouts"
}

func (e *ConfirmRequiredError) ExitCode() int { return 2 }

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
// with *InvalidOptsError (exit 2) — without this, statusPayload and
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

// resolvedStatus reports the wire-format release status (draft /
// completed / inProgress) a given Status + Track would produce after
// applying the ADR-0002 safe-default rule. Shared by both the upload
// and promote orchestrators so the rule is defined exactly once.
func resolvedStatus(track string, status Status) string {
	if status == StatusUnspecified {
		if track == TrackProduction {
			return "draft"
		}
		return "completed"
	}
	switch status {
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
	if status == StatusUnspecified {
		if track == TrackProduction {
			status = StatusDraft
		} else {
			status = StatusCompleted
		}
	}
	switch status {
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
	// AABPath is the local path to the artifact uploaded into the Edit. It
	// is historically named for the AAB common case but also carries an
	// APK path when Format is FormatAPK (ADR-0036); the release pipeline
	// after the upload step is identical either way.
	AABPath string
	// Format selects the upload endpoint for AABPath: FormatBundle (the
	// default, empty) routes through edits.bundles.upload, FormatAPK routes
	// through edits.apks.upload (ADR-0036). The command layer resolves this
	// from the file extension or an explicit --format override; the
	// versionCode both endpoints return drives the rest of the pipeline
	// unchanged.
	Format            string
	Status            Status
	UserFraction      float64
	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool

	// ExplicitEditID, when set, reuses an already-open explicit Edit
	// (`gplay edits begin`) instead of opening/committing a per-upload Edit:
	// the upload mutates the pinned Edit and leaves it open for the user to
	// `gplay edits commit` (docs/DESIGN.md §4). Empty is the implicit default.
	ExplicitEditID string

	// MappingPath, when set, uploads a ProGuard/R8 deobfuscation file (a
	// Mapping) alongside the AAB in the SAME Edit, keyed by the versionCode
	// the bundle upload returns. Empty means no mapping is uploaded. This
	// is the `releases upload --mapping` common case (#250); symbolicating
	// obfuscated crash stacks in Play vitals depends on it.
	MappingPath string

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
	VersionCode      int             `json:"versionCode"`
	Track            string          `json:"track"`
	ReleaseName      string          `json:"releaseName"`
	Status           string          `json:"status"`
	UserFraction     float64         `json:"userFraction,omitempty"`
	DefaultLanguage  string          `json:"defaultLanguage,omitempty"`
	Locales          []string        `json:"locales,omitempty"`
	RawTrackResponse json.RawMessage `json:"-"`

	// MappingUploaded reports whether a ProGuard/R8 mapping was uploaded
	// alongside the AAB (the --mapping path, #250). The ✓ confirmation
	// line surfaces it; the JSON pass-through stays the tracks.update body.
	MappingUploaded bool `json:"-"`
}

// Upload performs the upload flow inside a single Edit:
// edits.insert → (optional details.get for release notes) →
// bundles.upload → notes.Load → tracks.update → edits.commit.
// Release notes are fetched only when --release-notes or
// --release-notes-dir is supplied; otherwise the extra
// details.get round-trip is skipped.
//
// Production publishes (track == "production" with status that would
// reach real users) require opts.Confirm = true; otherwise Upload
// returns *ConfirmRequiredError (exit 2).
//
// opts.DryRun runs the input validation and previews what would be
// sent, without any HTTP. The returned Result has VersionCode=0 and
// ReleaseName="(dry-run)".
func Upload(ctx context.Context, hc *http.Client, opts Opts) (*Result, error) {
	// Caller-side validation runs first so dry-run callers still see
	// invalid-opts errors and live callers don't open an Edit only to
	// discover their flag combination was bad.
	if err := validateOpts(opts); err != nil {
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
		return nil, &ConfirmRequiredError{
			Track:  opts.Track,
			Status: resolvedStatus(opts.Track, opts.Status),
		}
	}

	result := &Result{Track: opts.Track}

	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID}, func(editID string) error {
		var localized []tracks.LocalizedText
		if opts.ReleaseNotes != "" || opts.ReleaseNotesDir != "" {
			// details.get is an extra round-trip; only do it when the
			// notes loader actually needs DefaultLanguage — i.e. when
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

		// Dispatch on artifact format: an APK rides edits.apks.upload, an
		// AAB (the default) rides edits.bundles.upload. Both return the
		// versionCode that drives the rest of the pipeline, so everything
		// below this line is format-agnostic (ADR-0036).
		var (
			versionCode int
			err         error
		)
		if opts.Format == FormatAPK {
			versionCode, err = apks.Upload(ctx, hc, opts.Package, editID, opts.AABPath)
		} else {
			versionCode, err = bundles.Upload(ctx, hc, opts.Package, editID, opts.AABPath)
		}
		if err != nil {
			return err
		}
		result.VersionCode = versionCode

		// Upload the ProGuard/R8 mapping in the SAME edit once the
		// versionCode is known. A failure here aborts the closure: in IMPLICIT
		// mode WithEdit auto-discards so we never commit a release whose mapping
		// silently failed to attach; in EXPLICIT mode the pinned Edit is left
		// open for the user to retry or discard. Either way the release is not
		// committed without its mapping — the whole point is readable crash
		// stacks (#250).
		if opts.MappingPath != "" {
			if _, err := mappings.Upload(ctx, hc, opts.Package, editID, versionCode, mappings.TypeProguard, opts.MappingPath); err != nil {
				return err
			}
			result.MappingUploaded = true
		}

		release := buildRelease(versionCode, opts)
		release.ReleaseNotes = localized
		_, raw, err := tracks.Update(ctx, hc, opts.Package, editID, opts.Track, release)
		if err != nil {
			return err
		}
		result.ReleaseName = release.Name
		result.Status = release.Status
		result.UserFraction = release.UserFraction
		result.RawTrackResponse = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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
	if opts.ReleaseNotes != "" && opts.ReleaseNotesDir != "" {
		return &InvalidOptsError{
			Message: "ReleaseNotes and ReleaseNotesDir are mutually exclusive — pick one",
		}
	}
	if opts.Status == StatusInProgress {
		if opts.UserFraction <= 0 || opts.UserFraction > 1.0 {
			return &InvalidOptsError{
				Message: "UserFraction must be in (0, 1] when Status is InProgress",
			}
		}
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

// buildRelease translates Opts + the discovered versionCode into a
// tracks.Release payload. The ADR-0002 safe-default rule and the
// status→userFraction mapping live in statusPayload, shared with
// buildPromoteRelease so the wire shape stays identical across upload
// and promote.
func buildRelease(versionCode int, opts Opts) tracks.Release {
	statusStr, userFraction := statusPayload(opts.Track, opts.Status, opts.UserFraction)
	codeStr := strconv.Itoa(versionCode)
	return tracks.Release{
		Name:         codeStr,
		Status:       statusStr,
		UserFraction: userFraction,
		VersionCodes: []string{codeStr},
	}
}

// TrackProduction is the canonical name of Google Play's production
// track. The safe-default rule in buildRelease tests against this
// constant rather than a string literal so the comparison is grep-able.
const TrackProduction = "production"

// Artifact format values for Opts.Format. FormatBundle is the empty
// default (edits.bundles.upload); FormatAPK routes the legacy path
// (edits.apks.upload, ADR-0036).
const (
	FormatBundle = "bundle"
	FormatAPK    = "apk"
)

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
	if opts.AABPath != "" {
		st, err := os.Stat(opts.AABPath)
		if err != nil {
			return nil, &dryRunError{msg: "artifact not accessible: " + err.Error()}
		}
		// Parity with the live path (bundles.Upload): a directory passes
		// Stat but cannot be streamed, so reject non-regular files here too
		// — otherwise --dry-run would green-light what the live upload rejects.
		if !st.Mode().IsRegular() {
			return nil, &dryRunError{msg: "artifact is not a regular file: " + opts.AABPath}
		}
	}

	if opts.MappingPath != "" {
		st, err := os.Stat(opts.MappingPath)
		if err != nil {
			return nil, &dryRunError{msg: "mapping not accessible: " + err.Error()}
		}
		// Parity with the live path (mappings.Upload): a directory passes
		// Stat but cannot be streamed, so reject non-regular files here too
		// — otherwise --dry-run would green-light what the live upload rejects.
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
	// flow uses, with a synthetic versionCode 0 since the real one is
	// only known after bundles.upload.
	release := buildRelease(0, opts)
	return &Result{
		Track:        opts.Track,
		VersionCode:  0,
		Status:       release.Status,
		UserFraction: release.UserFraction,
		ReleaseName:  "(dry-run)",
		Locales:      previewLocales,
	}, nil
}

// dryRunError is the validation-failure error for dry-run mode. It
// maps to exit 20 (client-side validation) per docs/DESIGN.md §9.
type dryRunError struct{ msg string }

func (e *dryRunError) Error() string { return "dry-run: " + e.msg }
func (e *dryRunError) ExitCode() int { return 20 }
