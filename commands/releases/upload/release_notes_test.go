package upload_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const emptyTextNote = "release notes for the default language (--release-notes) are empty (0 characters): write them, or drop this locale"

func TestRun_explicitEmptyReleaseNotes_exit20_noHTTP(t *testing.T) {
	modes := []struct {
		name string
		in   func(*upload.Input)
	}{
		{"internal", func(in *upload.Input) {}},
		{"dry-run", func(in *upload.Input) { in.DryRun = true }},
		{"production without confirm", func(in *upload.Input) { in.Track, in.Complete = "production", true }},
	}
	for _, m := range modes {
		rt, transport := newUploadTransport(uploadAPI{})
		rc, _ := newRC(t, transport)

		in := upload.Input{
			Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)},
			ReleaseNotes: "", ReleaseNotesSet: true,
		}
		m.in(&in)
		_, err := upload.Run(rc, in)
		if got := exit.For(err); got != 20 || err.Error() != emptyTextNote {
			t.Errorf("%s: Run = %v (exit %d), want %q exit 20", m.name, err, got, emptyTextNote)
		}
		if touched(rt) {
			t.Errorf("%s: RoundTripper saw calls: %v", m.name, apiCalls(rt))
		}
	}

	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)
	_, err := upload.Run(rc, upload.Input{
		Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)},
		ReleaseNotes: "", ReleaseNotesSet: true, ReleaseNotesDir: t.TempDir(),
	})
	if got := exit.For(err); got != 2 {
		t.Errorf("--release-notes \"\" with --release-notes-dir: exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw calls on a usage error: %v", apiCalls(rt))
	}
}
