package upload_test

// Artifact preflight (PRD #448) on `gplay releases expansion-files upload`.
// There is no package name to read out of an .obb, so this surface gets the
// container check only, and it catches the mistake that actually happens
// here: handing the command the app's build instead of its asset sidecar.

import (
	"strings"
	"testing"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
)

func TestRun_preflight_refusesAnAndroidPackageWhereAnOBBBelongs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  func(string) string
		found string
	}{
		{"aab", func(dir string) string { return artifacttest.AAB(t, dir, "assets.obb", "com.example.app") }, "an Android App Bundle (AAB)"},
		{"apk", func(dir string) string { return artifacttest.APK(t, dir, "assets.obb", "com.example.app") }, "an APK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &efRT{t: t}
			rc := newRC(t, rt)
			_, err := uploadcmd.Run(rc, uploadcmd.Input{
				Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: tc.path(t.TempDir()),
			})
			if got := exitOf(t, err); got != 20 {
				t.Fatalf("exit = %d, want 20; err=%v", got, err)
			}
			if len(rt.calls) != 0 {
				t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
			}
			if !strings.Contains(err.Error(), "found "+tc.found) {
				t.Errorf("refusal %q does not name what it found (%s)", err, tc.found)
			}
		})
	}
}

// TestRun_skipPreflight_uploadsAnAABAsAnExpansionFile asserts the escape
// hatch bypasses the container check here too: an unusual but legitimate
// artifact must never be blocked by gplay's own classifier.
func TestRun_skipPreflight_uploadsAnAABAsAnExpansionFile(t *testing.T) {
	rt := &efRT{t: t}
	rc := newRC(t, rt)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package:       "com.example.app",
		VersionCode:   142,
		Type:          "main",
		OBBPath:       artifacttest.AAB(t, t.TempDir(), "assets.obb", "com.example.app"),
		SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	if len(rt.calls) == 0 {
		t.Error("--skip-preflight must let the upload proceed")
	}
}
