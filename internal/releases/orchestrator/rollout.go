// Package orchestrator (rollout.go) owns the staged-rollout state machine:
// `gplay releases rollout / halt / resume / complete`. Each verb opens an
// Edit, reads the target track, picks the release to mutate, rewrites its
// status / userFraction via tracks.update, and commits — inheriting the
// auto-discard / DanglingEditError contract from edits.WithEdit.
//
// The release picker (pickTargetRelease) is intentionally separate from
// promote's pickSourceRelease: the two share a shape but differ in their
// not-found exit code (here 30 — "release not found" per issue #46; promote
// uses 60). Factoring the two into one shared picker is deferred to a later
// PR, so this file carries its own.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// ReleaseNotFoundError signals that the target track exists but holds no
// release matching the request: either it has no releases at all, or a
// supplied --version-code / --release-name matched none. Mapped to exit
// code 30 (API 4xx / not found) per issue #46 — distinct from promote's
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

	// UserFraction is the target rollout fraction in (0, 1]. Only Rollout
	// reads it; Halt / Resume / Complete ignore it (they preserve or fix
	// the fraction themselves).
	UserFraction float64

	KeepEditOnFailure bool

	// Confirm gates production-impacting transitions. Required (else
	// *ConfirmRequiredError, exit 2) when Track is "production" AND the
	// transition would expose the release to real users — rollout / resume
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
}

// Rollout sets the target release to status=inProgress at the requested
// userFraction (AC1). Used both to start a staged rollout and to ramp an
// in-progress one up to a larger fraction.
func Rollout(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	fraction := opts.UserFraction
	return applyState(ctx, hc, opts, stateTransition{verb: "rollout", status: "inProgress", fraction: &fraction})
}

// Halt freezes the rollout: status=halted with the current userFraction
// preserved (AC2), so a later Resume can pick up where it left off.
func Halt(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, stateTransition{verb: "halt", status: "halted"})
}

// Resume flips a halted release back to inProgress, leaving the userFraction
// untouched (AC3) — the rollout continues at the fraction it was halted at.
func Resume(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	return applyState(ctx, hc, opts, stateTransition{verb: "resume", status: "inProgress"})
}

// Complete ramps the release to a full rollout: userFraction=1.0 and
// status=completed (AC4).
func Complete(ctx context.Context, hc *http.Client, opts StateOpts) (*Result, error) {
	fraction := 1.0
	return applyState(ctx, hc, opts, stateTransition{verb: "complete", status: "completed", fraction: &fraction})
}

// stateTransition describes one verb's rewrite of the target release:
// status is always set; fraction (nil → preserve the release's current
// userFraction) is set only by rollout / complete. Applied by patching the
// raw release JSON (not by re-serializing a typed Release) so every other
// field — and every other coexisting release on the track — is preserved.
type stateTransition struct {
	verb     string
	status   string
	fraction *float64
}

// applyState is the shared choreography behind all four verbs: validate →
// (dry-run preview) → WithEdit{ tracks.get → pick → transition →
// tracks.update } → commit. Centralizing it keeps the auto-discard and
// exit-code semantics identical across the family.
func applyState(ctx context.Context, hc *http.Client, opts StateOpts, tr stateTransition) (*Result, error) {
	if err := validateStateOpts(opts, tr.verb); err != nil {
		return nil, err
	}
	// Dry-run is evaluated BEFORE the confirm gate so a production publish
	// can be previewed without satisfying --confirm (mirrors upload/promote).
	if opts.DryRun {
		return dryRunStateResult(opts, tr), nil
	}
	// Inherits ADR-0002's confirm guard: a transition that would reach real
	// users on production (rollout / resume / complete — not halt) requires
	// Confirm=true. Runs before any HTTP so a missing --confirm short-circuits
	// without opening an Edit.
	if transitionReachesProdUsers(opts.Track, tr.status) && !opts.Confirm {
		return nil, &ConfirmRequiredError{Track: opts.Track, Status: tr.status}
	}

	result := &Result{Track: opts.Track}
	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{KeepOnFailure: opts.KeepEditOnFailure}, func(editID string) error {
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
		// in the raw tracks.get JSON: every other release on the track, and
		// every field gplay does not model, survives byte-for-byte. A typed
		// re-serialization would drop coexisting releases (tracks.update is a
		// full replace) and unmodeled fields like countryTargeting.
		body, err := patchTrackRelease(rawTrack, idx, tr.status, tr.fraction)
		if err != nil {
			return err
		}
		_, raw, err := tracks.UpdateRaw(ctx, hc, opts.Package, editID, opts.Track, body)
		if err != nil {
			return err
		}

		// VersionCode is informational (which build was affected). A draft
		// placeholder may carry none; guard the index so we never panic.
		if len(target.VersionCodes) > 0 {
			if vc, convErr := strconv.Atoi(target.VersionCodes[0]); convErr == nil {
				result.VersionCode = vc
			}
		}
		result.ReleaseName = target.Name
		result.Status = tr.status
		if tr.fraction != nil {
			result.UserFraction = *tr.fraction
		} else {
			result.UserFraction = target.UserFraction // preserved (halt / resume)
		}
		result.RawTrackResponse = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// patchTrackRelease rewrites the status (and, when fraction != nil, the
// userFraction) of releases[idx] inside the raw tracks.get body, leaving all
// other releases and all other fields untouched. Non-target releases keep
// their exact bytes; the target keeps every field except the two the verb
// changes. The result is the body for tracks.update.
func patchTrackRelease(rawTrack json.RawMessage, idx int, status string, fraction *float64) ([]byte, error) {
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
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	target["status"] = statusJSON
	if fraction != nil {
		fractionJSON, err := json.Marshal(*fraction)
		if err != nil {
			return nil, err
		}
		target["userFraction"] = fractionJSON
	}

	patched, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	releases[idx] = patched
	releasesJSON, err := json.Marshal(releases)
	if err != nil {
		return nil, err
	}
	track["releases"] = releasesJSON
	return json.Marshal(track)
}

// validateStateOpts enforces the orchestrator-level preconditions every
// StateOpts caller (CLI, future MCP server, library user) must satisfy,
// before any Edit is opened. Mirrors validateOpts / validatePromoteOpts.
func validateStateOpts(opts StateOpts, verb string) error {
	if opts.Track == "" {
		return &InvalidOptsError{Message: "Track is required"}
	}
	if verb == "rollout" {
		if opts.UserFraction <= 0 || opts.UserFraction > 1.0 {
			return &InvalidOptsError{
				Message: "rollout --to fraction must be in (0, 1] (e.g. 0.05, 0.20)",
			}
		}
	}
	return nil
}

// transitionReachesProdUsers reports whether a transition to the given wire
// status on the given track would expose the release to real production
// users — the rollout-family analogue of requiresConfirm (upload / promote).
// inProgress / completed reach users; halted does not (it reduces exposure).
func transitionReachesProdUsers(track, status string) bool {
	if track != TrackProduction {
		return false
	}
	return status == "inProgress" || status == "completed"
}

// dryRunStateResult previews the transition without HTTP. It can show the
// status the verb sets and, for rollout / complete, the target fraction.
// Halt / resume preserve the live fraction (unknown without tracks.get), so
// the preview leaves UserFraction at 0 — the renderer hides a zero fraction
// rather than implying a 0% rollout.
func dryRunStateResult(opts StateOpts, tr stateTransition) *Result {
	var userFraction float64
	if tr.fraction != nil {
		userFraction = *tr.fraction
	}
	return &Result{
		Track:        opts.Track,
		VersionCode:  0,
		Status:       tr.status,
		UserFraction: userFraction,
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
