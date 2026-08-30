package upload_test

// Artifact preflight (PRD #448) on `gplay releases sharing upload`. Internal
// App Sharing bypasses tracks and the Edit lifecycle, but it is still keyed
// by package, so both checks apply: the container must be the format
// --format resolved to, and the build must belong to the app named.

import (
	"strings"
	"testing"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/sharing/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
)

func TestRun_preflight_refusesAContainerThatIsNotTheResolvedFormat(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)

	// An AAB behind an .apk name: extension auto-detect says "apk".
	p := artifacttest.AAB(t, t.TempDir(), "app.apk", "com.example.app")
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: p})
	if got := exitOf(t, err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
	}
	for _, want := range []string{"expected an APK", "found an Android App Bundle (AAB)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestRun_preflight_refusesAnotherAppsBuild(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)

	p := artifacttest.APK(t, t.TempDir(), "app.apk", "com.other.app")
	_, err := uploadcmd.Run(rc, uploadcmd.Input{Package: "com.example.app", ArtifactPath: p})
	if got := exitOf(t, err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
	}
	if !strings.Contains(err.Error(), "package mismatch") {
		t.Errorf("refusal %q does not name the mismatch", err)
	}
}

func TestRun_skipPreflight_restoresPreCheckBehaviour(t *testing.T) {
	rt := &sharingRT{t: t}
	rc, _ := newRC(t, rt)

	p := artifacttest.WriteFile(t, t.TempDir(), "app.apk", []byte("not an artifact at all"))
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package: "com.example.app", ArtifactPath: p, SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	if !strings.Contains(rt.uploadURL, "/artifacts/apk") {
		t.Errorf("--skip-preflight must let the upload proceed; url=%q", rt.uploadURL)
	}
}
