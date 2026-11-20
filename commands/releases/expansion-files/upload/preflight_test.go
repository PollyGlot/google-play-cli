package upload_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	uploadcmd "github.com/PollyGlot/google-play-cli/commands/releases/expansion-files/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifact"
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
			rt, transport := newEFTransport()
			rc := newRC(t, transport)
			_, err := uploadcmd.Run(rc, uploadcmd.Input{
				Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: tc.path(t.TempDir()),
			})
			if got := exitOf(t, err); got != 20 {
				t.Fatalf("exit = %d, want 20; err=%v", got, err)
			}
			if touched(rt) {
				t.Errorf("a refused artifact must make no network call; calls=%v", apiCalls(rt))
			}
			if !strings.Contains(err.Error(), "found "+tc.found) {
				t.Errorf("refusal %q does not name what it found (%s)", err, tc.found)
			}
		})
	}
}

func TestRun_skipPreflight_uploadsAnAABAsAnExpansionFile(t *testing.T) {
	rt, transport := newEFTransport()
	rc := newRC(t, transport)
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package:       "com.example.app",
		VersionCode:   142,
		Type:          "main",
		OBBPath:       artifacttest.AAB(t, t.TempDir(), "assets.obb", "com.example.app"),
		SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	if !touched(rt) {
		t.Error("--skip-preflight must let the upload proceed")
	}
}

func TestRun_preflight_overTheMemberCapDegradesInsteadOfPassingAsAnOBB(t *testing.T) {
	_, transport := newEFTransport()
	rc := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	p := artifacttest.APKWith(t, t.TempDir(), "assets.obb", "com.example.app",
		artifacttest.FillerMembers(artifact.MaxZipEntries+1))
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: p,
	}); err != nil {
		t.Fatalf("Run: %v, want an unclassified container to degrade rather than be refused", err)
	}
	if !strings.Contains(stderr.String(), "unchecked") {
		t.Errorf("stderr %q should say the container check did not happen, not let it pass as satisfied", stderr.String())
	}
}

func TestRun_dryRunSkipPreflight_stillRefusesAMissingArtifact(t *testing.T) {
	rt, transport := newEFTransport()
	rc := newRC(t, transport)

	_, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package:       "com.example.app",
		VersionCode:   142,
		Type:          "main",
		OBBPath:       filepath.Join(t.TempDir(), "never-built.obb"),
		DryRun:        true,
		SkipPreflight: true,
	})
	if got := exitOf(t, err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a missing artifact must make no network call; calls=%v", apiCalls(rt))
	}
}
