package promote_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_explicitEmptyReleaseNotes_exit20_noHTTP(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		rt := newPromoteFake(promoteAPI{})
		rc, _ := newRC(t, rt)

		_, err := promote.Run(rc, promote.Input{
			Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
			ReleaseNotes: "", ReleaseNotesSet: true, DryRun: dryRun,
		})
		if got := exit.For(err); got != 20 {
			t.Errorf("dry-run=%v: exit.For(err) = %d, want 20; err=%v", dryRun, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "empty (0 characters)") {
			t.Errorf("dry-run=%v: error %q does not say the note is empty", dryRun, err)
		}
		if touched(rt) {
			t.Errorf("dry-run=%v: RoundTripper saw calls: %v", dryRun, apiCalls(rt))
		}
	}
}
