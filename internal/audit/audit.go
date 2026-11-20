package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

const (
	CheckLingeringDrafts = "lingering-drafts"

	CheckLocaleDrift = "locale-drift"

	CheckEmptyReleaseNotes = "empty-release-notes"

	CheckNoProductionRelease = "no-production-release"
)

type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

const ProductionTrack = "production"

type Snapshot struct {
	Package  string
	Tracks   []tracks.Track
	Listings []listings.Listing
}

type Finding struct {
	Package  string            `json:"package"`
	Check    string            `json:"check"`
	Severity Severity          `json:"severity"`
	Message  string            `json:"message"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

type Check struct {
	ID          string
	Severity    Severity
	Description string
	Eval        func(s Snapshot, sweep Context) []Finding
}

type Context struct {
	LocaleUnion   []string
	SnapshotCount int
}

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

func Checks() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	return out
}

func IDs() []string {
	ids := make([]string, 0, len(registry))
	for _, c := range registry {
		ids = append(ids, c.ID)
	}
	return ids
}

type UnknownCheckError struct{ ID string }

func (e *UnknownCheckError) Error() string {
	return fmt.Sprintf("unknown check %q: valid checks are %s", e.ID, strings.Join(IDs(), ", "))
}

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

func Evaluate(s Snapshot, checks []Check, sweep Context) []Finding {
	var out []Finding
	for _, c := range checks {
		out = append(out, c.Eval(s, sweep)...)
	}
	return out
}

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

func evalLingeringDrafts(s Snapshot, _ Context) []Finding {
	var out []Finding
	for _, t := range s.Tracks {
		var drafts []string
		for _, r := range t.Releases {
			if r.Status == tracks.StatusDraft {
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

func evalEmptyReleaseNotes(s Snapshot, _ Context) []Finding {
	var out []Finding
	for _, t := range s.Tracks {
		for _, r := range t.Releases {
			if r.Status == tracks.StatusDraft {
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

func releaseLabel(r tracks.Release) string {
	if r.Name != "" {
		return r.Name
	}
	if len(r.VersionCodes) > 0 {
		return strings.Join(r.VersionCodes, "+")
	}
	return "(unnamed release)"
}
