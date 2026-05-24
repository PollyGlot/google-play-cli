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
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
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

// resolvedPublishStatus reports the wire-format status a given Opts
// would produce after applying the safe-default rule. Used by both the
// confirm guard and the dry-run preview.
func resolvedPublishStatus(opts Opts) string {
	s := opts.Status
	if s == StatusUnspecified {
		if opts.Track == TrackProduction {
			return "draft"
		}
		return "completed"
	}
	switch s {
	case StatusDraft:
		return "draft"
	case StatusCompleted:
		return "completed"
	case StatusInProgress:
		return "inProgress"
	}
	return ""
}

// requiresConfirm reports whether the upload would publish to real
// users on production. Draft uploads (explicit or safe-default) do
// not need confirmation. Non-production tracks affect only testers.
func requiresConfirm(opts Opts) bool {
	if opts.Track != TrackProduction {
		return false
	}
	status := resolvedPublishStatus(opts)
	return status == "completed" || status == "inProgress"
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
	Package           string
	Track             string
	AABPath           string
	Status            Status
	UserFraction      float64
	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool

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
	if requiresConfirm(opts) && !opts.Confirm {
		return nil, &ConfirmRequiredError{
			Track:  opts.Track,
			Status: resolvedPublishStatus(opts),
		}
	}

	if opts.DryRun {
		return dryRunResult(opts)
	}

	result := &Result{Track: opts.Track}

	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure}, func(editID string) error {
		var localized []tracks.LocalizedText
		if opts.ReleaseNotes != "" || opts.ReleaseNotesDir != "" {
			lang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
			if err != nil {
				return err
			}
			result.DefaultLanguage = lang
			loaded, err := notes.Load(notes.Opts{
				Text:            opts.ReleaseNotes,
				Dir:             opts.ReleaseNotesDir,
				DefaultLanguage: lang,
			})
			if err != nil {
				return err
			}
			localized = make([]tracks.LocalizedText, 0, len(loaded))
			for _, n := range loaded {
				localized = append(localized, tracks.LocalizedText{Language: n.Locale, Text: n.Text})
				result.Locales = append(result.Locales, n.Locale)
			}
		}

		versionCode, err := bundles.Upload(ctx, hc, opts.Package, editID, opts.AABPath)
		if err != nil {
			return err
		}
		result.VersionCode = versionCode

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

// buildRelease translates Opts + the discovered versionCode into a
// tracks.Release payload. Owns the ADR-0002 safe-default rule: when
// Status is unspecified, target production defaults to draft and every
// other track defaults to completed/1.0.
func buildRelease(versionCode int, opts Opts) tracks.Release {
	status := opts.Status
	userFraction := opts.UserFraction
	if status == StatusUnspecified {
		if opts.Track == TrackProduction {
			status = StatusDraft
		} else {
			status = StatusCompleted
		}
	}
	statusStr := ""
	switch status {
	case StatusDraft:
		statusStr = "draft"
		userFraction = 0 // omitted by the json tag's omitempty
	case StatusCompleted:
		statusStr = "completed"
		userFraction = 1.0
	case StatusInProgress:
		statusStr = "inProgress"
		// userFraction passes through from Opts.
	}
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

// dryRunResult validates the inputs Upload would consume without any
// HTTP and returns a preview Result describing the planned payload.
// The Edit lifecycle, AAB upload, and tracks.update are all skipped.
func dryRunResult(opts Opts) (*Result, error) {
	if opts.AABPath != "" {
		if _, err := os.Stat(opts.AABPath); err != nil {
			return nil, &dryRunError{msg: "AAB not accessible: " + err.Error()}
		}
	}
	if opts.ReleaseNotesDir != "" {
		st, err := os.Stat(opts.ReleaseNotesDir)
		if err != nil {
			return nil, &dryRunError{msg: "release-notes-dir not accessible: " + err.Error()}
		}
		if !st.IsDir() {
			return nil, &dryRunError{msg: "release-notes-dir is not a directory: " + opts.ReleaseNotesDir}
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
	}, nil
}

// dryRunError is the validation-failure error for dry-run mode. It
// maps to exit 20 (client-side validation) per docs/DESIGN.md §9.
type dryRunError struct{ msg string }

func (e *dryRunError) Error() string { return "dry-run: " + e.msg }
func (e *dryRunError) ExitCode() int { return 20 }
