package orchestrator

import (
	"encoding/json"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// ReleaseState is the status a release is sent with, together with the
// userFraction that status allows. TrackRelease.userFraction in
// docs/discovery/androidpublisher_v3.json can only be set on an inProgress
// or halted release, and only in (0, 1): Play refuses a completed release
// that carries one, and refuses 1.0 on any release. A full rollout is the
// completed status, which carries no fraction.
//
// Only the constructors build a state, and the fraction is fixed with it, so
// neither illegal pair can reach tracks.update. The zero ReleaseState is the
// unspecified request: the ADR-0002 safe default resolves it per track.
// Halted is never built here: halt keeps the live release's fraction as Play
// returned it (see moveStatus).
type ReleaseState struct {
	status   string
	fraction float64
}

// Draft is a release that reaches no user until it is promoted.
func Draft() ReleaseState { return ReleaseState{status: "draft"} }

// Completed is a full rollout: every user, and no fraction on the wire.
func Completed() ReleaseState { return ReleaseState{status: "completed"} }

// InProgress is a staged rollout reaching fraction of users. It is the one
// place the (0, 1) bound is checked, for every command that takes --staged.
func InProgress(fraction float64) (ReleaseState, error) {
	if !(fraction > 0 && fraction < 1) {
		return ReleaseState{}, &InvalidOptsError{
			Message: "fraction must be strictly between 0 and 1, got " + strconv.FormatFloat(fraction, 'g', -1, 64),
		}
	}
	return ReleaseState{status: "inProgress", fraction: fraction}, nil
}

// RequestedState maps the --draft / --complete / --staged flags of
// `releases upload` and `releases promote` to a ReleaseState: the zero one
// when none is set, so the safe default applies. More than one flag, or a
// --staged fraction outside (0, 1), is a usage error (exit 2); the latter
// points at --complete, since a full rollout is not a staged one.
func RequestedState(draft, complete, staged bool, fraction float64) (ReleaseState, error) {
	set := 0
	for _, b := range []bool{draft, complete, staged} {
		if b {
			set++
		}
	}
	if set > 1 {
		return ReleaseState{}, exit.Usagef("--draft, --complete, and --staged are mutually exclusive")
	}
	switch {
	case draft:
		return Draft(), nil
	case complete:
		return Completed(), nil
	case staged:
		s, err := InProgress(fraction)
		if err != nil {
			return ReleaseState{}, exit.Usagef("--staged: %v; for a full rollout pass --complete", err)
		}
		return s, nil
	}
	return ReleaseState{}, nil
}

// Status is the wire status, "" for the unspecified request.
func (s ReleaseState) Status() string { return s.status }

// UserFraction is the wire userFraction, 0 when the status carries none: the
// omitempty tag on tracks.Release.UserFraction then leaves the key out.
func (s ReleaseState) UserFraction() float64 { return s.fraction }

// resolve applies the ADR-0002 safe default to the unspecified request:
// production gets a draft, every other track a completed release.
func (s ReleaseState) resolve(track string) ReleaseState {
	if s.status != "" {
		return s
	}
	if track == TrackProduction {
		return Draft()
	}
	return Completed()
}

// releaseEdit is one rollout-family verb's rewrite of the target release,
// in one of two forms: writeState sets the whole state (rollout sets a
// fraction, complete removes it), moveStatus changes the status and keeps the
// live fraction (halt, resume). Keeping, setting and removing the fraction
// therefore exclude one another by construction.
type releaseEdit interface {
	status() string
	// apply rewrites the target release's fields in place.
	apply(release map[string]json.RawMessage) error
	// resultFraction is the fraction the release ends with, given the one it
	// had (0 when unknown, as on a dry run).
	resultFraction(live float64) float64
}

type writeState struct{ to ReleaseState }

func (w writeState) status() string { return w.to.status }

func (w writeState) apply(release map[string]json.RawMessage) error {
	if err := setStatus(release, w.to.status); err != nil {
		return err
	}
	if w.to.fraction == 0 {
		delete(release, "userFraction")
		return nil
	}
	raw, err := json.Marshal(w.to.fraction)
	if err != nil {
		return err
	}
	release["userFraction"] = raw
	return nil
}

func (w writeState) resultFraction(float64) float64 { return w.to.fraction }

type moveStatus struct{ to string }

func (m moveStatus) status() string { return m.to }

func (m moveStatus) apply(release map[string]json.RawMessage) error {
	return setStatus(release, m.to)
}

func (m moveStatus) resultFraction(live float64) float64 { return live }

func setStatus(release map[string]json.RawMessage, status string) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	release["status"] = raw
	return nil
}
