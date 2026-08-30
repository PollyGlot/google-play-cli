// Package audit_test exercises the check evaluators against fixture
// resources. The engine is HTTP-free by design, so these tests need no
// transport at all: they are the table-driven half of PRD #449's testing
// decisions, and the command's RoundTripper tests cover the sweep.
package audit_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/audit"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// checkIDs collapses findings to their check IDs, in order, for assertion.
func checkIDs(fs []audit.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Check)
	}
	return out
}

// one runs a single named check over a snapshot, so a table case asserts one
// evaluator rather than the whole registry.
func one(t *testing.T, id string, s audit.Snapshot, ctx audit.Context) []audit.Finding {
	t.Helper()
	checks, err := audit.Select([]string{id}, nil)
	if err != nil {
		t.Fatalf("Select(%q): %v", id, err)
	}
	return audit.Evaluate(s, checks, ctx)
}

// TestLingeringDrafts covers the first check: one finding per TRACK holding
// drafts (not per release), silence for a track whose releases are all live,
// and a draft that carries no name still being nameable in the evidence.
func TestLingeringDrafts(t *testing.T) {
	cases := []struct {
		name       string
		snapshot   audit.Snapshot
		wantCount  int
		wantInMsg  string
		wantTrack  string
		wantNoFind bool
	}{
		{
			name: "draft on one track",
			snapshot: audit.Snapshot{Package: "com.a", Tracks: []tracks.Track{
				{Track: "internal", Releases: []tracks.Release{{Name: "1.2.0", Status: "draft"}}},
				{Track: "production", Releases: []tracks.Release{{Name: "1.1.0", Status: "completed"}}},
			}},
			wantCount: 1,
			wantInMsg: "1.2.0",
			wantTrack: "internal",
		},
		{
			name: "two drafts on one track collapse into one finding",
			snapshot: audit.Snapshot{Package: "com.a", Tracks: []tracks.Track{
				{Track: "alpha", Releases: []tracks.Release{
					{Name: "2.0", Status: "draft"},
					{Name: "2.1", Status: "draft"},
				}},
			}},
			wantCount: 1,
			wantInMsg: "2 draft release(s)",
			wantTrack: "alpha",
		},
		{
			name: "unnamed draft is labelled by version codes",
			snapshot: audit.Snapshot{Package: "com.a", Tracks: []tracks.Track{
				{Track: "beta", Releases: []tracks.Release{{Status: "draft", VersionCodes: []string{"42"}}}},
			}},
			wantCount: 1,
			wantInMsg: "42",
			wantTrack: "beta",
		},
		{
			name: "no drafts is clean",
			snapshot: audit.Snapshot{Package: "com.a", Tracks: []tracks.Track{
				{Track: "production", Releases: []tracks.Release{{Name: "1.0", Status: "completed"}}},
			}},
			wantNoFind: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := one(t, audit.CheckLingeringDrafts, tc.snapshot, audit.Context{})
			if tc.wantNoFind {
				if len(got) != 0 {
					t.Fatalf("findings = %v, want none", checkIDs(got))
				}
				return
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tc.wantCount, got)
			}
			f := got[0]
			if f.Severity != audit.SeverityWarning {
				t.Errorf("severity = %q, want warning", f.Severity)
			}
			if !strings.Contains(f.Message, tc.wantInMsg) {
				t.Errorf("message = %q, want it to mention %q", f.Message, tc.wantInMsg)
			}
			if f.Evidence["track"] != tc.wantTrack {
				t.Errorf("evidence track = %q, want %q", f.Evidence["track"], tc.wantTrack)
			}
		})
	}
}

// TestLocaleDrift covers the cross-app check, including its two silences: a
// complete locale set, and a sweep too small for the union to mean anything.
func TestLocaleDrift(t *testing.T) {
	full := audit.Snapshot{Package: "com.full", Listings: []listings.Listing{
		{Language: "en-US"}, {Language: "fr-FR"}, {Language: "de-DE"},
	}}
	narrow := audit.Snapshot{Package: "com.narrow", Listings: []listings.Listing{{Language: "en-US"}}}

	t.Run("missing locales are reported as info", func(t *testing.T) {
		ctx := audit.BuildContext([]audit.Snapshot{full, narrow})
		got := one(t, audit.CheckLocaleDrift, narrow, ctx)
		if len(got) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(got), got)
		}
		if got[0].Severity != audit.SeverityInfo {
			t.Errorf("severity = %q, want info (a narrower locale set is often deliberate)", got[0].Severity)
		}
		if miss := got[0].Evidence["missing"]; miss != "de-DE,fr-FR" {
			t.Errorf("evidence missing = %q, want the sorted de-DE,fr-FR", miss)
		}
	})

	t.Run("complete locale set is clean", func(t *testing.T) {
		ctx := audit.BuildContext([]audit.Snapshot{full, narrow})
		if got := one(t, audit.CheckLocaleDrift, full, ctx); len(got) != 0 {
			t.Fatalf("findings = %+v, want none", got)
		}
	})

	t.Run("single-app sweep stays silent", func(t *testing.T) {
		ctx := audit.BuildContext([]audit.Snapshot{narrow})
		if got := one(t, audit.CheckLocaleDrift, narrow, ctx); len(got) != 0 {
			t.Fatalf("findings = %+v, want none: with one app the union IS its own locale set", got)
		}
	})
}

// TestEmptyReleaseNotes covers the notes check: absent notes, blank text, and
// the two silences (notes present, and a draft, which the drafts check owns).
func TestEmptyReleaseNotes(t *testing.T) {
	cases := []struct {
		name      string
		release   tracks.Release
		wantFind  bool
		wantInMsg string
	}{
		{
			name:      "shipped release with no notes",
			release:   tracks.Release{Name: "1.0", Status: "completed"},
			wantFind:  true,
			wantInMsg: "no release notes",
		},
		{
			name: "blank note text counts as missing",
			release: tracks.Release{Name: "1.0", Status: "inProgress", ReleaseNotes: []tracks.LocalizedText{
				{Language: "en-US", Text: "  "},
			}},
			wantFind:  true,
			wantInMsg: "en-US",
		},
		{
			name: "notes present is clean",
			release: tracks.Release{Name: "1.0", Status: "completed", ReleaseNotes: []tracks.LocalizedText{
				{Language: "en-US", Text: "Bug fixes"},
			}},
		},
		{
			name:    "draft without notes is not this check's business",
			release: tracks.Release{Name: "2.0", Status: "draft"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := audit.Snapshot{Package: "com.a", Tracks: []tracks.Track{
				{Track: "production", Releases: []tracks.Release{tc.release}},
			}}
			got := one(t, audit.CheckEmptyReleaseNotes, s, audit.Context{})
			if !tc.wantFind {
				if len(got) != 0 {
					t.Fatalf("findings = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d findings, want 1: %+v", len(got), got)
			}
			if !strings.Contains(got[0].Message, tc.wantInMsg) {
				t.Errorf("message = %q, want it to mention %q", got[0].Message, tc.wantInMsg)
			}
		})
	}
}

// TestNoProductionRelease covers the three states of the production track:
// present with a release (clean), present but empty, and absent entirely. The
// last two are one check told apart by the evidence.
func TestNoProductionRelease(t *testing.T) {
	cases := []struct {
		name       string
		tracksIn   []tracks.Track
		wantFind   bool
		wantReason string
	}{
		{
			name:     "production with a release is clean",
			tracksIn: []tracks.Track{{Track: "production", Releases: []tracks.Release{{Name: "1.0", Status: "completed"}}}},
		},
		{
			name:       "production configured but empty",
			tracksIn:   []tracks.Track{{Track: "production"}},
			wantFind:   true,
			wantReason: "empty",
		},
		{
			name:       "production absent from tracks.list",
			tracksIn:   []tracks.Track{{Track: "internal", Releases: []tracks.Release{{Name: "1.0", Status: "completed"}}}},
			wantFind:   true,
			wantReason: "absent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := audit.Snapshot{Package: "com.a", Tracks: tc.tracksIn}
			got := one(t, audit.CheckNoProductionRelease, s, audit.Context{})
			if !tc.wantFind {
				if len(got) != 0 {
					t.Fatalf("findings = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d findings, want 1: %+v", len(got), got)
			}
			if got[0].Evidence["reason"] != tc.wantReason {
				t.Errorf("evidence reason = %q, want %q", got[0].Evidence["reason"], tc.wantReason)
			}
		})
	}
}

// TestSelect pins the enable/disable semantics: default is every check, an
// unknown ID on either side is an error (a typo'd --skip-check must never
// silently leave the check running), and exclude is applied after include.
func TestSelect(t *testing.T) {
	all, err := audit.Select(nil, nil)
	if err != nil {
		t.Fatalf("Select(nil,nil): %v", err)
	}
	if len(all) != len(audit.IDs()) {
		t.Errorf("default selection = %d checks, want all %d", len(all), len(audit.IDs()))
	}

	sel, err := audit.Select([]string{audit.CheckLingeringDrafts, audit.CheckLocaleDrift}, []string{audit.CheckLocaleDrift})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel) != 1 || sel[0].ID != audit.CheckLingeringDrafts {
		t.Errorf("selection = %+v, want only lingering-drafts (exclude wins over include)", sel)
	}

	for _, tc := range []struct{ include, exclude []string }{
		{include: []string{"lingering-draft"}},
		{exclude: []string{"nope"}},
	} {
		if _, err := audit.Select(tc.include, tc.exclude); err == nil {
			t.Errorf("Select(%v,%v) = nil error, want UnknownCheckError", tc.include, tc.exclude)
		}
	}
}

// TestIDsAreFrozen guards the vocabulary a CI filter is written against: an ID
// may be retired, never renamed, so this test names the exact strings.
func TestIDsAreFrozen(t *testing.T) {
	want := []string{"lingering-drafts", "locale-drift", "empty-release-notes", "no-production-release"}
	got := audit.IDs()
	if len(got) != len(want) {
		t.Fatalf("IDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("IDs()[%d] = %q, want %q (check IDs are frozen vocabulary)", i, got[i], want[i])
		}
	}
}

// TestEvaluateRunsEveryCheck asserts the full registry over one drifted app,
// so a check silently dropped from the registry fails here.
func TestEvaluateRunsEveryCheck(t *testing.T) {
	drifted := audit.Snapshot{
		Package: "com.drift",
		Tracks: []tracks.Track{
			{Track: "internal", Releases: []tracks.Release{{Name: "9.0", Status: "draft"}}},
			{Track: "beta", Releases: []tracks.Release{{Name: "8.0", Status: "completed"}}},
		},
		Listings: []listings.Listing{{Language: "en-US"}},
	}
	peer := audit.Snapshot{Package: "com.peer", Listings: []listings.Listing{{Language: "en-US"}, {Language: "ja-JP"}}}

	got := audit.Evaluate(drifted, audit.Checks(), audit.BuildContext([]audit.Snapshot{drifted, peer}))
	want := map[string]bool{
		audit.CheckLingeringDrafts:     true,
		audit.CheckLocaleDrift:         true,
		audit.CheckEmptyReleaseNotes:   true,
		audit.CheckNoProductionRelease: true,
	}
	for _, f := range got {
		delete(want, f.Check)
	}
	if len(want) != 0 {
		t.Errorf("checks that produced no finding on a fully drifted app: %v (got %v)", want, checkIDs(got))
	}
}
