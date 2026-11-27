package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

type AmbiguousReleaseError struct {
	Track         string
	Candidates    []tracks.Release
	FilterApplied bool
}

func (e *AmbiguousReleaseError) Error() string {
	var b strings.Builder
	b.WriteString("track ")
	b.WriteString(e.Track)
	b.WriteString(" has multiple coexisting releases: ")
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

type PromoteOpts struct {
	Package   string
	FromTrack string
	ToTrack   string

	State ReleaseState

	UpdatePriority *int

	VersionCode int
	ReleaseName string

	ReleaseNotes      string
	ReleaseNotesSet   bool
	ReleaseNotesDir   string
	KeepEditOnFailure bool

	ExplicitEditID string

	Commit edits.CommitOptions

	Confirm bool

	DryRun bool

	Plan bool
}

type PromotePlan struct {
	From         string           `json:"from"`
	To           string           `json:"to"`
	Source       tracks.Release   `json:"source"`
	Status       string           `json:"status"`
	UserFraction float64          `json:"userFraction,omitempty"`
	Replaced     []tracks.Release `json:"replaced"`
}

type DestinationReadError struct {
	Err error
}

func (e *DestinationReadError) Error() string { return e.Err.Error() }
func (e *DestinationReadError) Unwrap() error { return e.Err }

func Promote(ctx context.Context, hc *http.Client, opts PromoteOpts) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if err := notes.Precheck(opts.ReleaseNotes, opts.ReleaseNotesDir); err != nil {
		return nil, err
	}

	if opts.DryRun {
		return dryRunPromoteResult(opts), nil
	}
	if opts.Plan {
		return planPromote(ctx, hc, opts)
	}

	if requiresConfirm(opts.ToTrack, opts.State) && !opts.Confirm {
		return nil, confirmRequired(opts.State.resolve(opts.ToTrack).Status())
	}

	result := &Result{Track: opts.ToTrack}

	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		p, err := readPromotion(ctx, hc, opts, editID)
		if err != nil {
			return err
		}
		source := p.source
		result.VersionCode = p.versionCode
		for _, r := range p.replaced {
			result.ReplacedVersionCodes = append(result.ReplacedVersionCodes, versionCodes(r)...)
		}

		release := buildPromoteRelease(source, opts)
		release.ReleaseNotes, result.DefaultLanguage, err = promoteReleaseNotes(ctx, hc, opts, editID, source)
		if err != nil {
			return err
		}
		for _, n := range release.ReleaseNotes {
			result.Locales = append(result.Locales, n.Language)
		}
		_, raw, err := tracks.Update(ctx, hc, opts.Package, editID, opts.ToTrack, release)
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

type promotion struct {
	source      tracks.Release
	versionCode int
	replaced    []tracks.Release
}

func readPromotion(ctx context.Context, hc *http.Client, opts PromoteOpts, editID string) (promotion, error) {
	src, _, err := tracks.Get(ctx, hc, opts.Package, editID, opts.FromTrack)
	if err != nil {
		return promotion{}, err
	}
	source, err := pickSourceRelease(src.Releases, opts)
	if err != nil {
		return promotion{}, err
	}
	if len(source.VersionCodes) == 0 {
		return promotion{}, &EmptyVersionCodesError{Track: opts.FromTrack, ReleaseName: source.Name}
	}
	versionCode, err := strconv.Atoi(source.VersionCodes[0])
	if err != nil {
		return promotion{}, &InvalidOptsError{Message: "source release has non-numeric versionCode: " + source.VersionCodes[0]}
	}
	dst, _, err := tracks.Get(ctx, hc, opts.Package, editID, opts.ToTrack)
	if err != nil {
		return promotion{}, &DestinationReadError{Err: err}
	}
	return promotion{source: *source, versionCode: versionCode, replaced: dst.Releases}, nil
}

func promoteReleaseNotes(ctx context.Context, hc *http.Client, opts PromoteOpts, editID string, source tracks.Release) ([]tracks.LocalizedText, string, error) {
	if opts.ReleaseNotes == "" && opts.ReleaseNotesDir == "" {
		return source.ReleaseNotes, "", nil
	}
	loadOpts := notes.Opts{Text: opts.ReleaseNotes, Dir: opts.ReleaseNotesDir}
	if opts.ReleaseNotes != "" || hasDefaultTxt(opts.ReleaseNotesDir) {
		lang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
		if err != nil {
			return nil, "", err
		}
		loadOpts.DefaultLanguage = lang
	}
	loaded, err := notes.Load(loadOpts)
	if err != nil {
		return nil, "", err
	}
	if dropped := droppedSourceLocales(source.ReleaseNotes, loaded); len(dropped) > 0 {
		return nil, "", &InvalidOptsError{
			Message: "release-notes override would drop locales the source release carries: " +
				strings.Join(dropped, ", ") +
				". Add files for these locales to the override, or run without --release-notes(-dir) to carry over the source's notes verbatim.",
		}
	}
	releaseNotes := make([]tracks.LocalizedText, 0, len(loaded))
	for _, n := range loaded {
		releaseNotes = append(releaseNotes, tracks.LocalizedText{Language: n.Locale, Text: n.Text})
	}
	return releaseNotes, loadOpts.DefaultLanguage, nil
}

func planPromote(ctx context.Context, hc *http.Client, opts PromoteOpts) (*Result, error) {
	var p promotion
	read := func(editID string) error {
		var err error
		p, err = readPromotion(ctx, hc, opts, editID)
		if err != nil {
			return err
		}
		_, _, err = promoteReleaseNotes(ctx, hc, opts, editID, p.source)
		return err
	}
	var err error
	if opts.ExplicitEditID != "" {
		err = read(opts.ExplicitEditID)
	} else {
		err = edits.WithReadOnlyEdit(ctx, hc, opts.Package, read)
	}
	if err != nil {
		return nil, err
	}
	release := buildPromoteRelease(p.source, opts)
	plan := &PromotePlan{
		From:         opts.FromTrack,
		To:           opts.ToTrack,
		Source:       p.source,
		Status:       release.Status,
		UserFraction: release.UserFraction,
		Replaced:     append([]tracks.Release{}, p.replaced...),
	}
	return &Result{
		Track:        opts.ToTrack,
		VersionCode:  p.versionCode,
		ReleaseName:  p.source.Name,
		Status:       plan.Status,
		UserFraction: plan.UserFraction,
		Promotion:    plan,
	}, nil
}

func (opts PromoteOpts) Validate() error {
	if err := validateNotesSources(opts.ReleaseNotes, opts.ReleaseNotesSet, opts.ReleaseNotesDir); err != nil {
		return err
	}
	if err := validateUpdatePriority(opts.UpdatePriority); err != nil {
		return err
	}
	if opts.FromTrack == "" {
		return &InvalidOptsError{Message: "FromTrack is required"}
	}
	if opts.ToTrack == "" {
		return &InvalidOptsError{Message: "ToTrack is required"}
	}
	if opts.FromTrack == opts.ToTrack {
		return &InvalidOptsError{Message: "FromTrack and ToTrack are both " + opts.ToTrack + ": promote copies a release to another track"}
	}
	if err := notes.ValidateDirLocales(opts.ReleaseNotesDir); err != nil {
		return err
	}
	return nil
}

func dryRunPromoteResult(opts PromoteOpts) *Result {
	state := opts.State.resolve(opts.ToTrack)
	return &Result{
		Track:               opts.ToTrack,
		VersionCode:         opts.VersionCode,
		Requires:            safetyFlags(requiresConfirm(opts.ToTrack, opts.State)),
		Status:              state.Status(),
		UserFraction:        state.UserFraction(),
		InAppUpdatePriority: opts.UpdatePriority,
		ReleaseName:         "(dry-run)",
	}
}

func pickSourceRelease(releases []tracks.Release, opts PromoteOpts) (*tracks.Release, error) {
	if len(releases) == 0 {
		return nil, &InvalidOptsError{Message: "source track " + opts.FromTrack + " has no releases to promote"}
	}
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

func buildPromoteRelease(source tracks.Release, opts PromoteOpts) tracks.Release {
	state := opts.State.resolve(opts.ToTrack)
	priority := source.InAppUpdatePriority
	if opts.UpdatePriority != nil {
		priority = opts.UpdatePriority
	}
	var targeting json.RawMessage
	if opts.ToTrack == TrackProduction && state.UserFraction() > 0 {
		targeting = source.CountryTargeting
	}
	return tracks.Release{
		Name:                source.Name,
		Status:              state.Status(),
		UserFraction:        state.UserFraction(),
		VersionCodes:        source.VersionCodes,
		InAppUpdatePriority: priority,
		CountryTargeting:    targeting,
	}
}
