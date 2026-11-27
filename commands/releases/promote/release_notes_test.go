package promote_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const emptyTextNote = "release notes for the default language (--release-notes) are empty (0 characters): write them, or drop this locale"

func TestRun_explicitEmptyReleaseNotes_exit20_noHTTP(t *testing.T) {
	modes := []struct {
		name string
		in   func(*promote.Input)
	}{
		{"to beta", func(in *promote.Input) {}},
		{"dry-run", func(in *promote.Input) { in.DryRun = true }},
		{"plan", func(in *promote.Input) { in.Plan = true }},
		{"production without confirm", func(in *promote.Input) { in.ToTrack, in.Complete = "production", true }},
	}
	for _, m := range modes {
		rt := newPromoteFake(promoteAPI{})
		rc, _ := newRC(t, rt)

		in := promote.Input{
			Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
			ReleaseNotes: "", ReleaseNotesSet: true,
		}
		m.in(&in)
		_, err := promote.Run(rc, in)
		if got := exit.For(err); got != 20 || err.Error() != emptyTextNote {
			t.Errorf("%s: Run = %v (exit %d), want %q exit 20", m.name, err, got, emptyTextNote)
		}
		if touched(rt) {
			t.Errorf("%s: RoundTripper saw calls: %v", m.name, apiCalls(rt))
		}
	}
}
