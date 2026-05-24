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
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/bundles"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

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

// Opts is the input contract for Upload. Fields not yet honored by the
// tracer bullet (ReleaseNotes, ReleaseNotesDir, UserFraction,
// KeepEditOnFailure) are reserved for upcoming TDD blocks.
type Opts struct {
	Package           string
	Track             string
	AABPath           string
	Status            Status
	UserFraction      float64
	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool
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
func Upload(ctx context.Context, hc *http.Client, opts Opts) (*Result, error) {
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
