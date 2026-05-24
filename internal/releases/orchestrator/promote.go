// Package orchestrator (promote.go) owns the choreography of
// `gplay releases promote`: open an Edit, read the source track, pick
// the release to copy, attach it (same versionCode) to the destination
// track, and commit. Inherits the ADR-0002 safe-default rule (via
// statusPayload) and the auto-discard / DanglingEditError contract
// from edits.WithEdit — so production promotes default to draft, and
// any failure mid-Edit cleans up the orphan unless --keep-edit-on-failure.
package orchestrator

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

// AmbiguousReleaseError signals that the source track has multiple
// coexisting releases that map to a single promotion target. Mapped to
// exit code 60 (state conflict / ambiguous release target). The error
// lists the candidate versionCodes + names so the operator can pick.
//
// FilterApplied carries whether the caller already supplied
// --version-code or --release-name; the hint adapts so the operator
// is told to TIGHTEN the existing filter rather than to pass one
// they already passed.
type AmbiguousReleaseError struct {
	Track         string
	Candidates    []tracks.Release
	FilterApplied bool
}

func (e *AmbiguousReleaseError) Error() string {
	var b strings.Builder
	b.WriteString("source track ")
	b.WriteString(e.Track)
	b.WriteString(" has multiple coexisting releases — ")
	if e.FilterApplied {
		b.WriteString("the supplied --version-code/--release-name still matched more than one; tighten by combining both filters or pick a more specific value. Candidates: ")
	} else {
		b.WriteString("pass --version-code or --release-name to pick one. Candidates: ")
	}
	for i, r := range e.Candidates {
		if i > 0 {
			b.WriteString(", ")
		}
		name := r.Name
		if name == "" {
			name = "(unnamed)"
		}
		codes := strings.Join(r.VersionCodes, ",")
		b.WriteString(name)
		b.WriteString(" [")
		b.WriteString(r.Status)
		b.WriteString(", versionCodes=")
		b.WriteString(codes)
		b.WriteString("]")
	}
	return b.String()
}

func (e *AmbiguousReleaseError) ExitCode() int { return 60 }

// NoMatchingReleaseError signals that a --version-code or
// --release-name filter was supplied but did not match any release on
// the source track. Distinct from AmbiguousReleaseError so the
// operator's hint can be targeted ("nothing on the track matches"
// vs "pick one of these candidates"). Same exit code (60).
type NoMatchingReleaseError struct {
	Track       string
	VersionCode int
	ReleaseName string
	Available   []tracks.Release
}

func (e *NoMatchingReleaseError) Error() string {
	var b strings.Builder
	b.WriteString("no release on source track ")
	b.WriteString(e.Track)
	b.WriteString(" matches ")
	var parts []string
	if e.VersionCode != 0 {
		parts = append(parts, "--version-code="+strconv.Itoa(e.VersionCode))
	}
	if e.ReleaseName != "" {
		parts = append(parts, "--release-name="+e.ReleaseName)
	}
	b.WriteString(strings.Join(parts, " AND "))
	if len(e.Available) > 0 {
		b.WriteString(". Available: ")
		for i, r := range e.Available {
			if i > 0 {
				b.WriteString(", ")
			}
			name := r.Name
			if name == "" {
				name = "(unnamed)"
			}
			b.WriteString(name)
			b.WriteString(" [versionCodes=")
			b.WriteString(strings.Join(r.VersionCodes, ","))
			b.WriteString("]")
		}
	}
	return b.String()
}

func (e *NoMatchingReleaseError) ExitCode() int { return 60 }

// EmptyVersionCodesError signals that the picked source release has
// no versionCodes — legal on Google Play (e.g. a draft "name-only"
// placeholder created without an attached AAB) but there is nothing
// to promote. Mapped to exit 60 (state conflict): the source track
// state is unsuitable, not a CLI misuse.
type EmptyVersionCodesError struct {
	Track       string
	ReleaseName string
}

func (e *EmptyVersionCodesError) Error() string {
	name := e.ReleaseName
	if name == "" {
		name = "(unnamed)"
	}
	return "source release " + name + " on track " + e.Track + " has no versionCodes (likely a draft placeholder without an AAB); nothing to promote"
}

func (e *EmptyVersionCodesError) ExitCode() int { return 60 }

// PromoteOpts is the input contract for Promote. Mirrors Opts where
// the semantics overlap (Status / UserFraction / ReleaseNotes /
// Confirm / DryRun / KeepEditOnFailure) so callers of both flows can
// share flag-parsing helpers.
type PromoteOpts struct {
	Package   string
	FromTrack string
	ToTrack   string

	Status       Status
	UserFraction float64

	// VersionCode / ReleaseName disambiguate the source release when
	// multiple coexist on FromTrack (e.g. inProgress + halted). Exactly
	// one is needed in that case; if neither is provided and the source
	// is ambiguous, Promote returns *AmbiguousReleaseError (exit 60).
	VersionCode int
	ReleaseName string

	ReleaseNotes      string
	ReleaseNotesDir   string
	KeepEditOnFailure bool

	Confirm bool

	// DryRun validates inputs and computes what would be sent without
	// performing any HTTP. No Edit is opened, no source tracks.get is
	// issued, no destination tracks.update is sent. The returned Result
	// describes the planned payload with VersionCode=0 (the real value
	// would come from tracks.get on the source) and ReleaseName="(dry-run)".
	// Runs BEFORE the confirm guard so operators can preview a
	// production publish without committing to --confirm.
	DryRun bool
}

// Promote performs the promote flow inside a single Edit:
// edits.insert → tracks.get(FromTrack) → pick the source release →
// tracks.update(ToTrack) → edits.commit. The carried-over versionCode
// is what the destination release advertises; no AAB is re-uploaded.
//
// Safe-default rule from ADR-0002 applies to the destination: target
// production with Status=Unspecified produces a draft release; every
// other target defaults to completed/1.0. Explicit --draft / --complete
// / --staged override the default.
//
// When the source track carries multiple coexisting releases, the
// caller must pass VersionCode or ReleaseName; otherwise Promote
// refuses with *AmbiguousReleaseError (exit 60).
func Promote(ctx context.Context, hc *http.Client, opts PromoteOpts) (*Result, error) {
	// Orchestrator-level input validation. Runs before any HTTP so
	// library / future-MCP callers do not burn an Edit slot (a 24h
	// resource) on inputs the orchestrator can reject statically. The
	// CLI layer in commands/releases/promote/promote.go ALSO catches
	// these; both layers enforce the contract independently.
	if err := validatePromoteOpts(opts); err != nil {
		return nil, err
	}

	// Dry-run is evaluated BEFORE the --confirm guard so users can
	// preview a production publish (e.g. --to production --complete)
	// without having to satisfy the confirm guardrail. The preview
	// path performs no HTTP and has no side effects.
	if opts.DryRun {
		return dryRunPromoteResult(opts), nil
	}

	// Inherits ADR-0002's confirm guard: a production target that would
	// reach real users (Completed or InProgress) requires Confirm=true.
	// Runs before any HTTP so a missing --confirm short-circuits without
	// opening an Edit.
	if requiresConfirm(opts.ToTrack, opts.Status) && !opts.Confirm {
		return nil, &ConfirmRequiredError{
			Track:  opts.ToTrack,
			Status: resolvedStatus(opts.ToTrack, opts.Status),
		}
	}

	result := &Result{Track: opts.ToTrack}

	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure}, func(editID string) error {
		src, _, err := tracks.Get(ctx, hc, opts.Package, editID, opts.FromTrack)
		if err != nil {
			return err
		}
		source, err := pickSourceRelease(src.Releases, opts)
		if err != nil {
			return err
		}

		if len(source.VersionCodes) == 0 {
			return &EmptyVersionCodesError{Track: opts.FromTrack, ReleaseName: source.Name}
		}
		versionCode, err := strconv.Atoi(source.VersionCodes[0])
		if err != nil {
			return &InvalidOptsError{Message: "source release has non-numeric versionCode: " + source.VersionCodes[0]}
		}
		result.VersionCode = versionCode

		release := buildPromoteRelease(*source, opts)
		// Release-notes resolution:
		//   neither flag → carry over source notes verbatim (AC5 default)
		//   --release-notes / --release-notes-dir → drop source, load
		//     the override via notes.Load (with details.get only when
		//     the DefaultLanguage is actually needed)
		if opts.ReleaseNotes == "" && opts.ReleaseNotesDir == "" {
			release.ReleaseNotes = source.ReleaseNotes
			for _, n := range source.ReleaseNotes {
				result.Locales = append(result.Locales, n.Language)
			}
		} else {
			needLang := opts.ReleaseNotes != "" || hasDefaultTxt(opts.ReleaseNotesDir)
			loadOpts := notes.Opts{Text: opts.ReleaseNotes, Dir: opts.ReleaseNotesDir}
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
			// Refuse if the override would drop any locale the source
			// release already carried. The promote default (no flags)
			// is verbatim carry-over, so any --release-notes(-dir)
			// override that is MISSING locales the source had would
			// silently delete those locales on the destination. Notes
			// loss is invisible after the fact and very expensive to
			// recover, so we fail-loud rather than silent-drop.
			if dropped := droppedSourceLocales(source.ReleaseNotes, loaded); len(dropped) > 0 {
				return &InvalidOptsError{
					Message: "release-notes override would drop locales the source release carries: " +
						strings.Join(dropped, ", ") +
						". Add files for these locales to the override, or run without --release-notes(-dir) to carry over the source's notes verbatim.",
				}
			}
			release.ReleaseNotes = make([]tracks.LocalizedText, 0, len(loaded))
			for _, n := range loaded {
				release.ReleaseNotes = append(release.ReleaseNotes, tracks.LocalizedText{Language: n.Locale, Text: n.Text})
				result.Locales = append(result.Locales, n.Locale)
			}
		}
		_, raw, err := tracks.Update(ctx, hc, opts.Package, editID, opts.ToTrack, release)
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

// validatePromoteOpts enforces the orchestrator-level preconditions
// every PromoteOpts caller (CLI, future MCP server, library user) must
// satisfy. Mirrors validateOpts on the upload side. Centralizing the
// checks here keeps exit semantics stable: every misuse maps to
// *InvalidOptsError (exit 2), never to a deeper-layer surprise like a
// 404 or a panic.
func validatePromoteOpts(opts PromoteOpts) error {
	if err := validateStatusValue(opts.Status); err != nil {
		return err
	}
	if opts.FromTrack == "" {
		return &InvalidOptsError{Message: "FromTrack is required"}
	}
	if opts.ToTrack == "" {
		return &InvalidOptsError{Message: "ToTrack is required"}
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

// dryRunPromoteResult previews what Promote would send for the given
// opts, without any HTTP. The real VersionCode would come from
// tracks.get on the source — synthesized as 0 here. The Status /
// UserFraction reflect the ADR-0002 safe-default rule via
// statusPayload so the preview shows exactly what the wire payload
// would look like.
func dryRunPromoteResult(opts PromoteOpts) *Result {
	statusStr, userFraction := statusPayload(opts.ToTrack, opts.Status, opts.UserFraction)
	return &Result{
		Track:        opts.ToTrack,
		VersionCode:  0,
		Status:       statusStr,
		UserFraction: userFraction,
		ReleaseName:  "(dry-run)",
	}
}

// pickSourceRelease selects which release on the source track to
// promote. With a single release it is unambiguous. With more than one
// the caller must disambiguate via VersionCode or ReleaseName, or
// receive *AmbiguousReleaseError (exit 60). Empty list → InvalidOpts
// (exit 2) since the user explicitly named a track with nothing on it.
func pickSourceRelease(releases []tracks.Release, opts PromoteOpts) (*tracks.Release, error) {
	if len(releases) == 0 {
		return nil, &InvalidOptsError{Message: "source track " + opts.FromTrack + " has no releases to promote"}
	}
	// Filter by --version-code or --release-name when provided. If
	// neither is set, every release is a candidate.
	candidates := releases
	if opts.VersionCode != 0 || opts.ReleaseName != "" {
		filtered := make([]tracks.Release, 0, 1)
		for _, r := range releases {
			if opts.ReleaseName != "" && r.Name != opts.ReleaseName {
				continue
			}
			if opts.VersionCode != 0 && !containsVersionCode(r.VersionCodes, opts.VersionCode) {
				continue
			}
			filtered = append(filtered, r)
		}
		if len(filtered) == 0 {
			return nil, &NoMatchingReleaseError{
				Track:       opts.FromTrack,
				VersionCode: opts.VersionCode,
				ReleaseName: opts.ReleaseName,
				Available:   releases,
			}
		}
		candidates = filtered
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	return nil, &AmbiguousReleaseError{
		Track:         opts.FromTrack,
		Candidates:    candidates,
		FilterApplied: opts.VersionCode != 0 || opts.ReleaseName != "",
	}
}

// droppedSourceLocales reports the locales the source release carried
// that the loaded override does NOT cover. Used to refuse partial
// overrides that would silently delete source locales on the
// destination. Comparison is case-sensitive to match the API.
func droppedSourceLocales(sourceNotes []tracks.LocalizedText, loaded []notes.LocaleNote) []string {
	if len(sourceNotes) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(loaded))
	for _, n := range loaded {
		have[n.Locale] = struct{}{}
	}
	var dropped []string
	for _, s := range sourceNotes {
		if _, ok := have[s.Language]; !ok {
			dropped = append(dropped, s.Language)
		}
	}
	return dropped
}

// containsVersionCode reports whether the source release's
// VersionCodes (API-typed as []string) includes the int the caller
// passed via --version-code. The API normalizes to a single code per
// release in practice, but we handle the multi-code shape defensively.
func containsVersionCode(codes []string, want int) bool {
	for _, c := range codes {
		got, err := strconv.Atoi(c)
		if err != nil {
			continue
		}
		if got == want {
			return true
		}
	}
	return false
}

// buildPromoteRelease translates a source release + PromoteOpts into
// the tracks.Release payload sent on the destination track. Carries
// over the source's versionCode and name; the ADR-0002 safe-default
// rule and the status→userFraction mapping live in statusPayload,
// shared with buildRelease.
func buildPromoteRelease(source tracks.Release, opts PromoteOpts) tracks.Release {
	statusStr, userFraction := statusPayload(opts.ToTrack, opts.Status, opts.UserFraction)
	return tracks.Release{
		Name:         source.Name,
		Status:       statusStr,
		UserFraction: userFraction,
		VersionCodes: source.VersionCodes,
	}
}
