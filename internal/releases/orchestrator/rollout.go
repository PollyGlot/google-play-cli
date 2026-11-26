package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

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

type StateOpts struct {
	Package string
	Track   string

	VersionCode int
	ReleaseName string

	UserFraction float64

	KeepEditOnFailure bool

	ExplicitEditID string

	Commit edits.CommitOptions

	Confirm bool

	DryRun bool

	Plan bool
}

type TransitionPlan struct {
	Track        string           `json:"track"`
	Target       tracks.Release   `json:"target"`
	Status       string           `json:"status"`
	UserFraction float64          `json:"userFraction,omitempty"`
	Superseded   []tracks.Release `json:"superseded"`
	Kept         []tracks.Release `json:"kept"`
}

func Rollout(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	to, err := InProgress(opts.UserFraction)
	if err != nil {
		return nil, err
	}
	return applyState(ctx, hc, opts, writeState{to: to})
}

func Halt(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, moveStatus{to: tracks.StatusHalted})
}

func Resume(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, moveStatus{to: tracks.StatusInProgress})
}

func Complete(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, writeState{to: Completed()})
}

func applyState(ctx context.Context, hc *http.Client, opts StateOpts, edit releaseEdit) (*Result, error) {
	if opts.Track == "" {
		return nil, &InvalidOptsError{Message: "Track is required"}
	}
	if opts.DryRun {
		return dryRunStateResult(opts, edit), nil
	}
	if opts.Plan {
		return planState(ctx, hc, opts, edit)
	}
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

		drop := superseded(cur.Releases, idx, edit)
		body, err := patchTrackRelease(tracks.GetSource(opts.Package), rawTrack, idx, edit, drop)
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

func firstVersionCode(r tracks.Release) int {
	if codes := versionCodes(r); len(codes) > 0 {
		return codes[0]
	}
	return 0
}

func versionCodes(r tracks.Release) []int {
	var out []int
	for _, c := range r.VersionCodes {
		if vc, err := strconv.Atoi(c); err == nil {
			out = append(out, vc)
		}
	}
	return out
}

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

func patchTrackRelease(from api.Source, rawTrack json.RawMessage, idx int, edit releaseEdit, drop []int) ([]byte, error) {
	var track map[string]json.RawMessage
	if err := json.Unmarshal(rawTrack, &track); err != nil {
		return nil, from.DecodeFailure("decode track for update", err)
	}
	var releases []json.RawMessage
	if err := json.Unmarshal(track["releases"], &releases); err != nil {
		return nil, from.DecodeFailure("decode releases for update", err)
	}
	if idx < 0 || idx >= len(releases) {
		return nil, from.DecodeFailure(fmt.Sprintf("target release index %d out of range (%d releases)", idx, len(releases)), nil)
	}

	var target map[string]json.RawMessage
	if err := json.Unmarshal(releases[idx], &target); err != nil {
		return nil, from.DecodeFailure("decode target release for update", err)
	}
	if target == nil {
		return nil, from.DecodeFailure("target release is null", nil)
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

func transitionReachesProdUsers(track, status string) bool {
	if track != TrackProduction {
		return false
	}
	return status == tracks.StatusInProgress || status == tracks.StatusCompleted
}

func dryRunStateResult(opts StateOpts, edit releaseEdit) *Result {
	return &Result{
		Track:        opts.Track,
		VersionCode:  opts.VersionCode,
		Requires:     safetyFlags(transitionReachesProdUsers(opts.Track, edit.status())),
		Status:       edit.status(),
		UserFraction: edit.resultFraction(0),
		ReleaseName:  "(dry-run)",
	}
}

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
