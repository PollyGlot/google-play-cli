package edits_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// coded is an error carrying its own exit code, like every typed gplay error.
type coded int

func (c coded) Error() string { return "coded failure" }
func (c coded) ExitCode() int { return int(c) }

// TestDanglingEditError_exitCodeAndMessage pins the exit-code contract of a
// KeepOnFailure Edit (DESIGN §9): the wrapped failure's code wins, 60 only
// when the cause carries none, and the message keeps the Edit ID and cause.
func TestDanglingEditError_exitCodeAndMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"inherits a coded cause", coded(40), 40},
		{"inherits through wrapping", &api.Error{Operation: "tracks.update", StatusCode: 403, Message: "denied"}, 11},
		{"falls back to 60 for a plain cause", errors.New("boom"), 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &edits.DanglingEditError{EditID: "edit-7", Err: tc.err}
			if got := e.ExitCode(); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
			if got := exit.Classify(e).ExitCode; got != tc.want {
				t.Errorf("Classify().ExitCode = %d, want %d (the envelope must agree with the process)", got, tc.want)
			}
			msg := e.Error()
			for _, want := range []string{"edit edit-7 left open", tc.err.Error()} {
				if !strings.Contains(msg, want) {
					t.Errorf("Error() = %q, want it to contain %q", msg, want)
				}
			}
			if !errors.Is(e, tc.err) {
				t.Error("DanglingEditError must unwrap to its cause")
			}
		})
	}
}
