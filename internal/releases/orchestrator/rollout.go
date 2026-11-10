// Package orchestrator (rollout.go) owns the staged-rollout state machine:
// `gplay releases rollout / halt / resume / complete`. Each verb opens an
// Edit, reads the target track, picks the release to mutate, rewrites its
// status / userFraction via tracks.update, and commits: inheriting the
// auto-discard / DanglingEditError contract from edits.WithEdit.
//
// The release picker (pickTargetRelease) is intentionally separate from
// promote's pickSourceRelease: the two share a shape but differ in their
// not-found exit code (here 30: "release not found" per issue #46; promote
// uses 60). Factoring the two into one shared picker is deferred to a later
// PR, so this file carries its own.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// ReleaseNotFoundError signals that the target track exists but holds no
// release matching the request: either it has no releases at all, or a
// supplied --version-code / --release-name matched none. Mapped to exit
// code 30 (API 4xx / not found) per issue #46: distinct from promote's
// NoMatchingReleaseError (exit 60), which is why the rollout family carries
// its own picker and error type.
type ReleaseNotFoundError struct {
	Track       string
	VersionCode int
	ReleaseName string
	Available   []tracks.Release
}

func (e *ReleaseNotFoundError) Error() string {
	if e.VersionCode == 0 && e.ReleaseName == "" {
		return "track " + e.Track + " has no releases to operate on"
	}
	var b strings.Builder
	b.WriteString("no release on track ")
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

func (e *ReleaseNotFoundError) ExitCode() int { return 30 }

// StateOpts is the shared input contract for the rollout state-machine
// verbs (Rollout, Halt, Resume, Complete). Each targets the latest release
// on Track; when the track holds more than one coexisting release the
// caller must disambiguate with VersionCode or ReleaseName, else the verb
// refuses with *AmbiguousReleaseError (exit 60).
type StateOpts struct {
	Package string
	Track   string

	// VersionCode / ReleaseName disambiguate the target release when
	// multiple coexist on Track (e.g. inProgress + halted). Exactly one is
	// needed in that case.
	VersionCode int
	ReleaseName string

	// UserFraction is the target rollout fraction, strictly between 0 and 1
	// (InProgress checks it). Only Rollout reads it; Halt / Resume keep the
	// live fraction and Complete removes it.
	UserFraction float64

	KeepEditOnFailure bool

	// ExplicitEditID reuses an already-open explicit Edit (`gplay edits begin`)
	// instead of opening/committing a per-transition Edit (docs/DESIGN.md §4).
	// Empty is the implicit default.
	ExplicitEditID string

	// Commit carries the opt-in edits.commit parameters; the zero value keeps
	// Google's default. Unused with ExplicitEditID (`gplay edits commit` commits).
	Commit edits.CommitOptions

	// Confirm gates production-impacting transitions. Required (else an
	// *exit.SafetyFlagError naming --confirm, exit 3) when Track is
	// "production" AND the
	// transition would expose the release to real users: rollout / resume
	// / complete. Halt is exempt (it reduces exposure), and non-production
	// tracks never need it. Mirrors the upload / promote confirm gate
	// (ADR-0002) so operators learn the rule once.
	Confirm bool

	// DryRun validates inputs and previews the planned transition without
	// any HTTP: no Edit is opened, no tracks.get, no tracks.update. The
	// returned Result describes the status / userFraction the update would
	// set, with VersionCode=0 and ReleaseName="(dry-run)". Runs BEFORE the
	// confirm gate so a production publish can be previewed without --confirm.
	DryRun bool

	// Plan reads the live track and reports what the transition would do,
	// without writing: the target release, the state it would end in, and
	// the releases it would supersede or keep (#740). It reads inside
	// ExplicitEditID when one is pinned, else in a read-only Edit that is
	// always discarded. No tracks.update, no commit, so no confirm gate.
	Plan bool
}

// TransitionPlan is the outcome a state transition would have on its track,
// computed from the live track by the same picker and supersede rule the
// write path uses, so a plan cannot disagree with the update it previews.
type TransitionPlan struct {
	Track        string           `json:"track"`
	Target       tracks.Release   `json:"target"`
	Status       string           `json:"status"`
	UserFraction float64          `json:"userFraction,omitempty"`
	Superseded   []tracks.Release `json:"superseded"`
	Kept         []tracks.Release `json:"kept"`
}

// Rollout sets the target release to status=inProgress at the requested
// userFraction (AC1). Used both to start a staged rollout and to ramp an
// in-progress one up to a larger fraction.
func Rollout(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	to, err := InProgress(opts.UserFraction)
	if err != nil {
		return nil, err
	}
	return applyState(ctx, hc, opts, writeState{to: to})
}

// Halt freezes the rollout: status=halted with the current userFraction
// preserved (AC2), so a later Resume can pick up where it left off.
func Halt(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, moveStatus{to: tracks.StatusHalted})
}

// Resume flips a halted release back to inProgress, leaving the userFraction
// untouched (AC3): the rollout continues at the fraction it was halted at.
func Resume(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, moveStatus{to: tracks.StatusInProgress})
}

// Complete ramps the release to a full rollout: status=completed and no
// userFraction at all (AC4). The fraction the release carried while it was
// inProgress is removed, not overwritten: Play refuses a completed release
// that carries one ("COMPLETED release must not have fraction").
func Complete(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, writeState{to: Completed()})
}

// applyState is the shared choreography behind all four verbs: validate →
// (dry-run preview) → WithEdit{ tracks.get → pick → edit → tracks.update }
// → commit. Centralizing it keeps the auto-discard and exit-code semantics
// identical across the family.
func applyState(ctx context.Context, hc *http.Client, opts StateOpts, edit releaseEdit) (*Result, error) {
	if opts.Track == "" {
		return nil, &InvalidOptsError{Message: "Track is required"}
	}
	// Dry-run is evaluated BEFORE the confirm gate so a production publish
	// can be previewed without satisfying --confirm (mirrors upload/promote).
	if opts.DryRun {
		return dryRunStateResult(opts, edit), nil
	}
	if opts.Plan {
		return planState(ctx, hc, opts, edit)
	}
	// Inherits ADR-0002's confirm guard: a transition that would reach real
	// users on production (rollout / resume / complete, not halt) requires
	// Confirm=true. Runs before any HTTP so a missing --confirm short-circuits
	// without opening an Edit.
	if transitionReachesProdUsers(opts.Track, edit.status()) && !opts.Confirm {
		return nil, confirmRequired(edit.status())
	}

	result := &Result{Track: opts.Track}
	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure, ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		cur, rawTrack, err := tracks.Get(ctx, hc, opts.Package, editID, opts.Track)
		if err != nil {
			return err
		}
		idx, err := pickTargetRelease(cur.Releases, opts)
		if err != nil {
			return err
		}
		target := cur.Releases[idx]

		// Build the tracks.update body by patching ONLY the target release
		// in the raw tracks.get JSON: every other release on the track (bar a
		// superseded completed one), and every field gplay does not model,
		// survives byte-for-byte. A typed
		// re-serialization would drop coexisting releases (tracks.update is a
		// full replace) and unmodeled fields like countryTargeting.
		drop := superseded(cur.Releases, idx, edit)
		body, err := patchTrackRelease(rawTrack, idx, edit, drop)
		if err != nil {
			return err
		}
		_, raw, err := tracks.UpdateRaw(ctx, hc, opts.Package, editID, opts.Track, body)
		if err != nil {
			return err
		}

		result.VersionCode = firstVersionCode(target)
		result.ReleaseName = target.Name
		result.Status = edit.status()
		result.UserFraction = edit.resultFraction(target.UserFraction)
		for _, i := range drop {
			result.SupersededVersionCodes = append(result.SupersededVersionCodes, versionCodes(cur.Releases[i])...)
		}
		result.RawTrackResponse = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// planState reads the track and describes the transition without writing.
// The Result carries the plan, plus the target's versionCode, name and the
// state it would end in, so callers read one shape for both paths.
func planState(ctx context.Context, hc *http.Client, opts StateOpts, edit releaseEdit) (*Result, error) {
	var plan *TransitionPlan
	read := func(editID string) error {
		cur, _, err := tracks.Get(ctx, hc, opts.Package, editID, opts.Track)
		if err != nil {
			return err
		}
		idx, err := pickTargetRelease(cur.Releases, opts)
		if err != nil {
			return err
		}
		drop := superseded(cur.Releases, idx, edit)
		target := cur.Releases[idx]
		plan = &TransitionPlan{
			Track:        opts.Track,
			Target:       target,
			Status:       edit.status(),
			UserFraction: edit.resultFraction(target.UserFraction),
			Superseded:   []tracks.Release{},
			Kept:         []tracks.Release{},
		}
		for i, r := range cur.Releases {
			switch {
			case i == idx:
			case slices.Contains(drop, i):
				plan.Superseded = append(plan.Superseded, r)
			default:
				plan.Kept = append(plan.Kept, r)
			}
		}
		return nil
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
	result := &Result{
		Track:        opts.Track,
		ReleaseName:  plan.Target.Name,
		Status:       plan.Status,
		UserFraction: plan.UserFraction,
		Plan:         plan,
	}
	result.VersionCode = firstVersionCode(plan.Target)
	for _, r := range plan.Superseded {
		result.SupersededVersionCodes = append(result.SupersededVersionCodes, versionCodes(r)...)
	}
	return result, nil
}

// firstVersionCode is the versionCode a release is reported under, 0 when it
// carries none (a draft placeholder).
func firstVersionCode(r tracks.Release) int {
	if codes := versionCodes(r); len(codes) > 0 {
		return codes[0]
	}
	return 0
}

// versionCodes parses a release's versionCodes, skipping any that are not
// numeric.
func versionCodes(r tracks.Release) []int {
	var out []int
	for _, c := range r.VersionCodes {
		if vc, err := strconv.Atoi(c); err == nil {
			out = append(out, vc)
		}
	}
	return out
}

// superseded lists the releases a transition retires along with the target
// at idx: when the target ends completed, every other completed release on
// the track. Play holds at most one completed release per track and refuses
// a tracks.update that carries two ("Only one completed release is
// allowed"), so completing a rollout replaces the previous full rollout, as
// it does in the Play Console (#734). Halted, inProgress and draft releases
// are never superseded.
func superseded(releases []tracks.Release, idx int, edit releaseEdit) []int {
	if edit.status() != tracks.StatusCompleted {
		return nil
	}
	var drop []int
	for i, r := range releases {
		if i != idx && r.Status == tracks.StatusCompleted {
			drop = append(drop, i)
		}
	}
	return drop
}

// patchTrackRelease applies edit to releases[idx] inside the raw tracks.get
// body and removes the releases at the drop indexes, leaving all other
// releases and all other fields untouched. Kept releases other than the
// target keep their exact bytes; the target keeps every field except the
// ones the edit rewrites. The result is the body for tracks.update.
func patchTrackRelease(rawTrack json.RawMessage, idx int, edit releaseEdit, drop []int) ([]byte, error) {
	var track map[string]json.RawMessage
	if err := json.Unmarshal(rawTrack, &track); err != nil {
		return nil, fmt.Errorf("decode track for update: %w", err)
	}
	var releases []json.RawMessage
	if err := json.Unmarshal(track["releases"], &releases); err != nil {
		return nil, fmt.Errorf("decode releases for update: %w", err)
	}
	if idx < 0 || idx >= len(releases) {
		return nil, fmt.Errorf("target release index %d out of range (%d releases)", idx, len(releases))
	}

	var target map[string]json.RawMessage
	if err := json.Unmarshal(releases[idx], &target); err != nil {
		return nil, fmt.Errorf("decode target release for update: %w", err)
	}
	if err := edit.apply(target); err != nil {
		return nil, err
	}

	patched, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	releases[idx] = patched
	kept := make([]json.RawMessage, 0, len(releases))
	for i, r := range releases {
		if !slices.Contains(drop, i) {
			kept = append(kept, r)
		}
	}
	releasesJSON, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	track["releases"] = releasesJSON
	return json.Marshal(track)
}

// transitionReachesProdUsers reports whether a transition to the given wire
// status on the given track would expose the release to real production
// users: the rollout-family analogue of requiresConfirm (upload / promote).
// inProgress / completed reach users; halted does not (it reduces exposure).
func transitionReachesProdUsers(track, status string) bool {
	if track != TrackProduction {
		return false
	}
	return status == tracks.StatusInProgress || status == tracks.StatusCompleted
}

// dryRunStateResult previews the transition without HTTP. It can show the
// status the verb sets and, for rollout, the target fraction. Halt / resume
// keep the live fraction (unknown without tracks.get), so the preview leaves
// UserFraction at 0: the renderer hides a zero fraction rather than implying
// a 0% rollout.
func dryRunStateResult(opts StateOpts, edit releaseEdit) *Result {
	return &Result{
		Track:        opts.Track,
		VersionCode:  0,
		Status:       edit.status(),
		UserFraction: edit.resultFraction(0),
		ReleaseName:  "(dry-run)",
	}
}

// pickTargetRelease selects which release on the track each state verb acts
// on, returning its index into releases (so the caller can patch that exact
// release in the raw track body). One release → unambiguous. More than one →
// the caller must narrow it with VersionCode / ReleaseName, else
// *AmbiguousReleaseError (exit 60). Zero releases, or a filter that matches
// none → *ReleaseNotFoundError (exit 30). Deliberately a sibling of promote's
// pickSourceRelease rather than a shared helper (see the package-file header).
func pickTargetRelease(releases []tracks.Release, opts StateOpts) (int, error) {
	filterApplied := opts.VersionCode != 0 || opts.ReleaseName != ""
	if len(releases) == 0 {
		return -1, &ReleaseNotFoundError{Track: opts.Track, VersionCode: opts.VersionCode, ReleaseName: opts.ReleaseName}
	}
	var matches []int
	for i, r := range releases {
		if filterApplied {
			if opts.ReleaseName != "" && r.Name != opts.ReleaseName {
				continue
			}
			if opts.VersionCode != 0 && !containsVersionCode(r.VersionCodes, opts.VersionCode) {
				continue
			}
		}
		matches = append(matches, i)
	}
	if len(matches) == 0 {
		return -1, &ReleaseNotFoundError{
			Track:       opts.Track,
			VersionCode: opts.VersionCode,
			ReleaseName: opts.ReleaseName,
			Available:   releases,
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	candidates := make([]tracks.Release, 0, len(matches))
	for _, i := range matches {
		candidates = append(candidates, releases[i])
	}
	return -1, &AmbiguousReleaseError{
		Track:         opts.Track,
		Candidates:    candidates,
		FilterApplied: filterApplied,
	}
}
