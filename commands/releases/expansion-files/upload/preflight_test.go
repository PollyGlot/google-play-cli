package upload_test

// Artifact preflight (PRD #448) on `gplay releases expansion-files upload`.
// There is no package name to read out of an .obb, so this surface gets the
// container check only, and it catches the mistake that actually happens
// here: handing the command the app's build instead of its asset sidecar.

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

// TestRun_preflight_overTheMemberCapDegradesInsteadOfPassingAsAnOBB is the
// mirror image of the false refusal on `releases upload`: here the
// expectation IS "neither an AAB nor an APK", so an over-cap archive that
// answered "unknown" satisfied the check by accident, checked in name only.
// Unclassified is now its own state: the container check is skipped, and the
// user is told so rather than left believing gplay vouched for the file.
func TestRun_preflight_overTheMemberCapDegradesInsteadOfPassingAsAnOBB(t *testing.T) {
	rt := &efRT{t: t}
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	p := artifacttest.APKWith(t, t.TempDir(), "assets.obb", "com.example.app",
		artifacttest.FillerMembers(artifact.MaxZipEntries+1))
	if _, err := uploadcmd.Run(rc, uploadcmd.Input{
		Package: "com.example.app", VersionCode: 142, Type: "main", OBBPath: p,
	}); err != nil {
		t.Fatalf("Run: %v, want an unclassified container to degrade rather than be refused", err)
	}
	// The outcome cannot change here (refusing would be the false refusal
	// this fix removes), so what the fix buys is honesty: the note has to say
	// the container went unchecked, not merely that it was not classified
	// while the expectation quietly counted as met.
	if !strings.Contains(stderr.String(), "unchecked") {
		t.Errorf("stderr %q should say the container check did not happen, not let it pass as satisfied", stderr.String())
	}
}

// TestRun_dryRunSkipPreflight_stillRefusesAMissingArtifact asserts
// --skip-preflight lifts the container check and nothing else: this surface
// stat'd the file on --dry-run before the preflight existed, and a missing
// path must not become exit 0 with a payload preview.
func TestRun_dryRunSkipPreflight_stillRefusesAMissingArtifact(t *testing.T) {
	rt := &efRT{t: t}
	rc := newRC(t, rt)

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
	if len(rt.calls) != 0 {
		t.Errorf("a missing artifact must make no network call; calls=%v", rt.calls)
	}
}
