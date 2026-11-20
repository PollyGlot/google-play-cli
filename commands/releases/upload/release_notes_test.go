package upload_test

import (
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_explicitEmptyReleaseNotes_exit20_noHTTP(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		rt, transport := newUploadTransport(uploadAPI{})
		rc, _ := newRC(t, transport)

		_, err := upload.Run(rc, upload.Input{
			Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)},
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
