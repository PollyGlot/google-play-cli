package orchestrator_test

import (
	"math"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

func staged(t *testing.T, f float64) orchestrator.ReleaseState {
	t.Helper()
	s, err := orchestrator.InProgress(f)
	if err != nil {
		t.Fatalf("InProgress(%v): %v", f, err)
	}
	return s
}

func TestInProgress_acceptsOnlyFractionsStrictlyBetweenZeroAndOne(t *testing.T) {
	for _, f := range []float64{0.0001, 0.05, 0.5, 0.9999} {
		s := staged(t, f)
		if s.Status() != "inProgress" || s.UserFraction() != f {
			t.Errorf("InProgress(%v) = (%q, %v), want (inProgress, %v)", f, s.Status(), s.UserFraction(), f)
		}
	}
	for _, f := range []float64{0, 1, 1.5, -0.1, math.NaN(), math.Inf(1)} {
		if _, err := orchestrator.InProgress(f); exit.For(err) != 2 {
			t.Errorf("InProgress(%v) err = %v (exit %d), want exit 2", f, err, exit.For(err))
		}
	}
}

func TestCompletedAndDraft_carryNoFraction(t *testing.T) {
	for _, s := range []orchestrator.ReleaseState{orchestrator.Completed(), orchestrator.Draft()} {
		if s.UserFraction() != 0 {
			t.Errorf("%s carries fraction %v, want none", s.Status(), s.UserFraction())
		}
	}
}

func TestRequestedState_mapsTheStatusFlags(t *testing.T) {
	cases := []struct {
		name                       string
		draft, complete, stagedSet bool
		fraction                   float64
		wantStatus                 string
		wantFraction               float64
	}{
		{name: "no flag keeps the safe default", wantStatus: ""},
		{name: "draft", draft: true, wantStatus: "draft"},
		{name: "complete", complete: true, wantStatus: "completed"},
		{name: "staged", stagedSet: true, fraction: 0.2, wantStatus: "inProgress", wantFraction: 0.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := orchestrator.RequestedState(tc.draft, tc.complete, tc.stagedSet, tc.fraction)
			if err != nil {
				t.Fatalf("RequestedState: %v", err)
			}
			if s.Status() != tc.wantStatus || s.UserFraction() != tc.wantFraction {
				t.Errorf("state = (%q, %v), want (%q, %v)", s.Status(), s.UserFraction(), tc.wantStatus, tc.wantFraction)
			}
		})
	}
}

func TestRequestedState_stagedOne_isUsageErrorPointingAtComplete(t *testing.T) {
	_, err := orchestrator.RequestedState(false, false, true, 1.0)
	if got := exit.For(err); got != 2 {
		t.Fatalf("exit = %d, want 2; err = %v", got, err)
	}
	for _, want := range []string{"--staged", "--complete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %s", err, want)
		}
	}
}

func TestRequestedState_twoFlags_isUsageError(t *testing.T) {
	_, err := orchestrator.RequestedState(true, false, true, 0.1)
	if got := exit.For(err); got != 2 || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v (exit %d), want a mutually-exclusive usage error", err, got)
	}
}
