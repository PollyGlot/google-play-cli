// Package audit holds the check engine behind `gplay apps audit` (PRD #449):
// a read-only consistency sweep over a set of apps. It is deliberately
// HTTP-free: the command layer fetches one Snapshot per app through the
// existing read surfaces (edits.tracks.list, edits.listings.list) and this
// package turns those snapshots into Findings.
//
// The split is the point. Evaluation is pure and table-testable; the sweep,
// its per-app failures and its quota cost live in the command. It also keeps
// the Report a gplay-owned document: the audit composes several API
// resources, so there is no single upstream body to mirror and ADR-0003's
// verbatim rule does not apply (like `schema`, the JSON here is a shape gplay
// invented, which is why the command ships [experimental]).
//
// Check IDs are frozen vocabulary from day one: a CI filter written against
// `lingering-drafts` must keep working, so an ID is never renamed, only
// retired.
package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// Check IDs. Frozen vocabulary (PRD #449): these strings are what a CI job
// passes to --check / --skip-check and what it greps in the JSON report.
const (
	// CheckLingeringDrafts flags a track still holding a draft release: a
	// build someone uploaded and never shipped, invisible until a human
	// opens the Play Console.
	CheckLingeringDrafts = "lingering-drafts"

	// CheckLocaleDrift flags an app whose Listing locale set is a strict
	// subset of the locale set seen across the audited apps: the classic
	// "we added de-DE everywhere except this one" drift.
	CheckLocaleDrift = "locale-drift"

	// CheckEmptyReleaseNotes flags a shipped (non-draft) release carrying no
	// release notes at all, or a note whose text is blank.
	CheckEmptyReleaseNotes = "empty-release-notes"

	// CheckNoProductionRelease flags an app whose production track carries no
	// release.
	//
	// Named for what the API can actually prove. The PRD phrased this class
	// as "no RECENT production release", but the Track resource carries no
	// timestamp on a release (no publish date, no lastUpdated), so "stale"
	// is not computable from a read; "absent" is. Dating a production release
	// would need a surface gplay does not cover, which the PRD puts out of
	// scope.
	CheckNoProductionRelease = "no-production-release"
)

// Severity labels a Finding. Two levels only: `warning` is drift an operator
// would plausibly act on, `info` is a difference worth naming that may well
// be intentional (a locale set that is deliberately narrower, say). There is
// no `error` level: every Finding is a consistency observation, never a
// failure of the sweep itself, which is reported separately as a SweepError.
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// ProductionTrack is the track name the no-production-release check reads.
const ProductionTrack = "production"

// draftStatus is the API's release status for a build staged but not shipped.
const draftStatus = "draft"

// Snapshot is everything the checks need about one app: the raw material of
// one sweep step. The command fills it from one read-only Edit per app
// (tracks.list + listings.list); the checks never do I/O of their own.
type Snapshot struct {
	// Package is the Android package name the snapshot describes.
	Package string
	// Tracks is the edits.tracks.list result: every configured track with
	// every release coexisting on it.
	Tracks []tracks.Track
	// Listings is the edits.listings.list result: one entry per locale.
	Listings []listings.Listing
}

// Finding is one consistency observation about one app. Evidence is a small
// gplay-owned map (never an API body) naming the exact resource that triggered
// the check, so an agent can act without re-reading the whole app.
type Finding struct {
	Package  string            `json:"package"`
	Check    string            `json:"check"`
	Severity Severity          `json:"severity"`
	Message  string            `json:"message"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

// Check is one registered consistency check. Eval receives the app's own
// Snapshot plus the sweep-wide context (needed by locale-drift, which is only
// meaningful across apps) and returns zero or more Findings.
type Check struct {
	ID          string
	Severity    Severity
	Description string
	Eval        func(s Snapshot, sweep Context) []Finding
}

// Context carries the cross-app facts a check needs. Today only locale-drift
// uses it: an app's locale set only reads as "drifted" relative to the other
// apps in the same sweep.
type Context struct {
	// LocaleUnion is the union of Listing locales over every app whose
	// snapshot was read successfully in this sweep, sorted.
	LocaleUnion []string
	// SnapshotCount is how many apps contributed to LocaleUnion. Below 2 the
	// union is just the single app's own locale set, so locale-drift stays
	// silent rather than emitting a tautologically empty result.
	SnapshotCount int
}

// registry is the ordered, frozen check set. Order here is the order checks
// run and the order the report lists them, so a report diff between two runs
// stays readable.
var registry = []Check{
	{
		ID:          CheckLingeringDrafts,
		Severity:    SeverityWarning,
		Description: "a track still holds a draft release",
		Eval:        evalLingeringDrafts,
	},
	{
		ID:          CheckLocaleDrift,
		Severity:    SeverityInfo,
		Description: "the app's Listing locales are a subset of the account's locale set",
		Eval:        evalLocaleDrift,
	},
	{
		ID:          CheckEmptyReleaseNotes,
		Severity:    SeverityWarning,
		Description: "a shipped release carries no release notes",
		Eval:        evalEmptyReleaseNotes,
	},
	{
		ID:          CheckNoProductionRelease,
		Severity:    SeverityWarning,
		Description: "the production track carries no release",
		Eval:        evalNoProductionRelease,
	},
}

// Checks returns the registered check set in run order. The slice is a copy:
// the registry is frozen vocabulary, not caller-mutable state.
func Checks() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	return out
}

// IDs returns every registered check ID in run order.
func IDs() []string {
	ids := make([]string, 0, len(registry))
	for _, c := range registry {
		ids = append(ids, c.ID)
	}
	return ids
}

// UnknownCheckError names a --check / --skip-check value that matches no
// registered ID, and lists the valid ones. The command maps it to exit 2: a
// typo'd check ID is CLI misuse, and failing loudly beats silently running a
// smaller check set than the operator asked for.
type UnknownCheckError struct{ ID string }

func (e *UnknownCheckError) Error() string {
	return fmt.Sprintf("unknown check %q: valid checks are %s", e.ID, strings.Join(IDs(), ", "))
}

// Select resolves the --check / --skip-check pair into the checks to run, in
// registry order. Empty include means "every check"; exclude is applied after
// include, so `--check a,b --skip-check b` runs only `a`. An unknown ID on
// EITHER side is an error: silently ignoring a typo in --skip-check would run
// a check the operator believed they had turned off.
func Select(include, exclude []string) ([]Check, error) {
	known := make(map[string]bool, len(registry))
	for _, c := range registry {
		known[c.ID] = true
	}
	for _, id := range append(append([]string{}, include...), exclude...) {
		if !known[id] {
			return nil, &UnknownCheckError{ID: id}
		}
	}

	wanted := make(map[string]bool, len(registry))
	if len(include) == 0 {
		for _, c := range registry {
			wanted[c.ID] = true
		}
	} else {
		for _, id := range include {
			wanted[id] = true
		}
	}
	for _, id := range exclude {
		delete(wanted, id)
	}

	out := make([]Check, 0, len(registry))
	for _, c := range registry {
		if wanted[c.ID] {
			out = append(out, c)
		}
	}
	return out, nil
}

// Evaluate runs checks over one Snapshot and returns the Findings in check
// order. Exported so the command layer never re-implements the loop and so a
// caller can evaluate a fixture without a sweep.
func Evaluate(s Snapshot, checks []Check, sweep Context) []Finding {
	var out []Finding
	for _, c := range checks {
		out = append(out, c.Eval(s, sweep)...)
	}
	return out
}

// BuildContext derives the sweep-wide Context from every snapshot read
// successfully. Apps that failed mid-sweep contribute nothing: their locales
// are unknown, and guessing would turn an API failure into a false Finding on
// their peers.
func BuildContext(snapshots []Snapshot) Context {
	seen := map[string]bool{}
	for _, s := range snapshots {
		for _, l := range s.Listings {
			if l.Language != "" {
				seen[l.Language] = true
			}
		}
	}
	union := make([]string, 0, len(seen))
	for l := range seen {
		union = append(union, l)
	}
	sort.Strings(union)
	return Context{LocaleUnion: union, SnapshotCount: len(snapshots)}
}

// evalLingeringDrafts emits one Finding per track holding at least one draft
// release, naming the draft release(s). One Finding per TRACK rather than per
// release: the operator's gesture (ship it or discard it) is per track, and a
// track with three drafts is one problem, not three.
func evalLingeringDrafts(s Snapshot, _ Context) []Finding {
	var out []Finding
	for _, t := range s.Tracks {
		var drafts []string
		for _, r := range t.Releases {
			if r.Status == draftStatus {
				drafts = append(drafts, releaseLabel(r))
			}
		}
		if len(drafts) == 0 {
			continue
		}
		out = append(out, Finding{
			Package:  s.Package,
			Check:    CheckLingeringDrafts,
			Severity: SeverityWarning,
			Message: fmt.Sprintf("track %q holds %d draft release(s): %s",
				t.Track, len(drafts), strings.Join(drafts, ", ")),
			Evidence: map[string]string{
				"track":    t.Track,
				"releases": strings.Join(drafts, ","),
			},
		})
	}
	return out
}

// evalLocaleDrift emits one Finding when the app's Listing locale set misses
// locales present elsewhere in the sweep. Severity is info, not warning: a
// narrower locale set is often deliberate (a regional app), so this reports a
// difference rather than asserting a mistake.
//
// It stays silent when fewer than two snapshots contributed to the union: with
// one app the union IS that app's locale set, so the check could only ever say
// "no drift", which is not the same as "checked". The what-ran section still
// records that the check ran.
func evalLocaleDrift(s Snapshot, sweep Context) []Finding {
	if sweep.SnapshotCount < 2 || len(sweep.LocaleUnion) == 0 {
		return nil
	}
	have := make(map[string]bool, len(s.Listings))
	for _, l := range s.Listings {
		have[l.Language] = true
	}
	var missing []string
	for _, l := range sweep.LocaleUnion {
		if !have[l] {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []Finding{{
		Package:  s.Package,
		Check:    CheckLocaleDrift,
		Severity: SeverityInfo,
		Message: fmt.Sprintf("Listing is missing %d locale(s) present on other audited apps: %s",
			len(missing), strings.Join(missing, ", ")),
		Evidence: map[string]string{
			"missing": strings.Join(missing, ","),
			"union":   strings.Join(sweep.LocaleUnion, ","),
		},
	}}
}

// evalEmptyReleaseNotes emits one Finding per shipped release with no usable
// release notes. "Shipped" means any status other than draft: a draft with no
// notes yet is normal work-in-progress, and drafts are already the subject of
// their own check. A note whose text is blank counts as missing: the field
// exists but the user-visible string does not.
func evalEmptyReleaseNotes(s Snapshot, _ Context) []Finding {
	var out []Finding
	for _, t := range s.Tracks {
		for _, r := range t.Releases {
			if r.Status == draftStatus {
				continue
			}
			blank := blankNoteLocales(r.ReleaseNotes)
			if len(r.ReleaseNotes) > 0 && len(blank) == 0 {
				continue
			}
			ev := map[string]string{"track": t.Track, "release": releaseLabel(r)}
			msg := fmt.Sprintf("release %q on track %q has no release notes", releaseLabel(r), t.Track)
			if len(blank) > 0 {
				ev["locales"] = strings.Join(blank, ",")
				msg = fmt.Sprintf("release %q on track %q has blank release notes for: %s",
					releaseLabel(r), t.Track, strings.Join(blank, ", "))
			}
			out = append(out, Finding{
				Package:  s.Package,
				Check:    CheckEmptyReleaseNotes,
				Severity: SeverityWarning,
				Message:  msg,
				Evidence: ev,
			})
		}
	}
	return out
}

// blankNoteLocales returns the locales whose note text is empty once trimmed.
func blankNoteLocales(notes []tracks.LocalizedText) []string {
	var blank []string
	for _, n := range notes {
		if strings.TrimSpace(n.Text) == "" {
			label := n.Language
			if label == "" {
				label = "(no language)"
			}
			blank = append(blank, label)
		}
	}
	return blank
}

// evalNoProductionRelease emits a Finding when the production track is absent
// from tracks.list or present with an empty releases array. Both mean the same
// thing to an operator (nothing has ever shipped to production), so they share
// one check and are told apart by the evidence.
func evalNoProductionRelease(s Snapshot, _ Context) []Finding {
	for _, t := range s.Tracks {
		if t.Track != ProductionTrack {
			continue
		}
		if len(t.Releases) > 0 {
			return nil
		}
		return []Finding{{
			Package:  s.Package,
			Check:    CheckNoProductionRelease,
			Severity: SeverityWarning,
			Message:  "the production track carries no release",
			Evidence: map[string]string{"track": ProductionTrack, "reason": "empty"},
		}}
	}
	return []Finding{{
		Package:  s.Package,
		Check:    CheckNoProductionRelease,
		Severity: SeverityWarning,
		Message:  "the production track is not configured",
		Evidence: map[string]string{"track": ProductionTrack, "reason": "absent"},
	}}
}

// releaseLabel names a release for a human: its API name when set, else its
// version codes, else a placeholder. A release with neither is legal on the
// wire and must still be nameable in a Finding.
func releaseLabel(r tracks.Release) string {
	if r.Name != "" {
		return r.Name
	}
	if len(r.VersionCodes) > 0 {
		return strings.Join(r.VersionCodes, "+")
	}
	return "(unnamed release)"
}
