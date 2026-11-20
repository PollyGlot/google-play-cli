package orchestrator

import (
	"encoding/json"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

type ReleaseState struct {
	status   string
	fraction float64
}

func Draft() ReleaseState { return ReleaseState{status: tracks.StatusDraft} }

func Completed() ReleaseState { return ReleaseState{status: tracks.StatusCompleted} }

func InProgress(fraction float64) (ReleaseState, error) {
	if !(fraction > 0 && fraction < 1) {
		return ReleaseState{}, &InvalidOptsError{
			Message: "fraction must be strictly between 0 and 1, got " + strconv.FormatFloat(fraction, 'g', -1, 64),
		}
	}
	return ReleaseState{status: tracks.StatusInProgress, fraction: fraction}, nil
}

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

func (s ReleaseState) Status() string { return s.status }

func (s ReleaseState) UserFraction() float64 { return s.fraction }

func (s ReleaseState) resolve(track string) ReleaseState {
	if s.status != "" {
		return s
	}
	if track == TrackProduction {
		return Draft()
	}
	return Completed()
}

type releaseEdit interface {
	status() string
	apply(release map[string]json.RawMessage) error
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
		delete(release, "countryTargeting")
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
