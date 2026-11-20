package create_test

import (
	"path/filepath"
	"strings"
	"testing"

	createcmd "github.com/PollyGlot/google-play-cli/commands/customapps/create"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
)

func TestRun_preflight_refusesAFileThatIsNotAnAndroidPackage(t *testing.T) {
	rt := &customRT{t: t}
	rc, _ := newRC(t, rt)

	in := validInput(artifacttest.WriteFile(t, t.TempDir(), "app.aab", []byte("a screenshot, not a build")))
	_, err := createcmd.Run(rc, in)
	if got := exitOf(t, err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
	}
	if !strings.Contains(err.Error(), "expected an Android App Bundle (AAB) or an APK") {
		t.Errorf("refusal %q does not name both accepted containers", err)
	}
}

func TestRun_preflight_acceptsAnAPKAsWellAsAnAAB(t *testing.T) {
	rt := &customRT{t: t}
	rc, _ := newRC(t, rt)

	in := validInput(artifacttest.APK(t, t.TempDir(), "app.apk", "com.example.app"))
	if _, err := createcmd.Run(rc, in); err != nil {
		t.Fatalf("Run with an APK: %v", err)
	}
}

func TestRun_skipPreflight_restoresPreCheckBehaviour(t *testing.T) {
	rt := &customRT{t: t}
	rc, _ := newRC(t, rt)

	in := validInput(artifacttest.WriteFile(t, t.TempDir(), "app.aab", []byte("not an artifact at all")))
	in.SkipPreflight = true
	if _, err := createcmd.Run(rc, in); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	if len(rt.calls) == 0 {
		t.Error("--skip-preflight must let the upload proceed")
	}
}

func TestRun_dryRunSkipPreflight_stillRefusesAMissingArtifact(t *testing.T) {
	rt := &customRT{t: t}
	rc, _ := newRC(t, rt)

	in := validInput(filepath.Join(t.TempDir(), "never-built.aab"))
	in.DryRun = true
	in.SkipPreflight = true
	_, err := createcmd.Run(rc, in)
	if got := exitOf(t, err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a missing artifact must make no network call; calls=%v", rt.calls)
	}
}
