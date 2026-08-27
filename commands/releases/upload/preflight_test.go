package upload_test

// Artifact preflight (PRD #448) on `gplay releases upload`: the local check
// that runs before any Edit is opened and before the first upload byte leaves
// the machine. It shares the suite's uploadRT / newRC harness, so every
// assertion below is also an assertion that nothing reached the network.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifact"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

// TestRun_preflight_refusesAPKPassedToABundleUpload asserts the preflight
// refuses a container that is not the format the invocation resolved to,
// naming both sides so an agent can self-correct.
func TestRun_preflight_refusesAPKPassedToABundleUpload(t *testing.T) {
	// A real APK container behind an .aab name, so extension auto-detect
	// resolves "bundle" and only the container check can catch it.
	p := artifacttest.APK(t, t.TempDir(), "app.aab", "com.example.app")
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: p})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
	}
	for _, want := range []string{"expected an Android App Bundle (AAB)", "found an APK", "--skip-preflight"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

// TestRun_preflight_refusesAnotherAppsBuild asserts the package the artifact
// declares is compared to the package being released: app A's release can
// never receive app B's build.
func TestRun_preflight_refusesAnotherAppsBuild(t *testing.T) {
	p := artifacttest.AAB(t, t.TempDir(), "app.aab", "com.other.app")
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: p})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a refused artifact must make no network call; calls=%v", rt.calls)
	}
	for _, want := range []string{"package mismatch", `"com.example.app"`, `"com.other.app"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

// TestRun_skipPreflight_restoresPreCheckBehaviour asserts the escape hatch is
// total: an artifact the preflight would refuse on both counts uploads
// exactly as it did before the check existed, with the identical request
// sequence. This is the parser-gap release valve, so it must never be partial.
func TestRun_skipPreflight_restoresPreCheckBehaviour(t *testing.T) {
	p := artifacttest.WriteFile(t, t.TempDir(), "app.aab", []byte("not an artifact at all"))
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-skip",
		versionCode:        7,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"7","status":"completed","versionCodes":["7"]}]}`,
	}
	rc, _ := newRC(t, rt)

	if _, err := upload.Run(rc, upload.Input{
		Package:       "com.example.app",
		Track:         "internal",
		AABPath:       p,
		SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-skip/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-skip/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-skip/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-skip:commit",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
}

// TestRun_preflight_dryRunReportsWhatItFound asserts a rehearsal names the
// container and the package it read, on stderr, without a ✓ and without any
// network call: a preview that stayed silent would say nothing about the
// check it just ran.
func TestRun_preflight_dryRunReportsWhatItFound(t *testing.T) {
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package: "com.example.app",
		Track:   "internal",
		AABPath: writeFakeAAB(t),
		DryRun:  true,
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run must make no network call; calls=%v", rt.calls)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run must not write a ✓ line: %q", stderr.String())
	}
	for _, want := range []string{"preflight", "com.example.app", "dry-run"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("dry-run stderr %q does not name %q", stderr.String(), want)
		}
	}
}

// TestRun_preflight_unreadableManifestDegradesRatherThanRefuses asserts a gap
// in gplay's own manifest readers never blocks a legitimate release: the
// container check still applies, the package check is skipped, and the reason
// is stated on stderr.
func TestRun_preflight_unreadableManifestDegradesRatherThanRefuses(t *testing.T) {
	p := artifacttest.Zip(t, t.TempDir(), "app.aab", map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": []byte("\xff\xff not a protobuf manifest"),
	})
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-degraded",
		versionCode:        8,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"8","status":"completed","versionCodes":["8"]}]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: p}); err != nil {
		t.Fatalf("Run: %v, want the upload to proceed on a degraded parse", err)
	}
	if !strings.Contains(stderr.String(), "preflight") {
		t.Errorf("stderr %q does not report the degraded parse", stderr.String())
	}
}

// TestRun_preflight_anAssetRichBundleOverTheMemberCapStillUploads is the
// regression that matters most on this surface: a real AAB can carry more zip
// members than the classifier walks, and gplay declining to walk it is a
// statement about gplay, not about the artifact. When the cap answered
// "unknown", this upload died at exit 20 on "found neither an AAB nor an
// APK", which is the false refusal ADR-0046 rules out.
func TestRun_preflight_anAssetRichBundleOverTheMemberCapStillUploads(t *testing.T) {
	p := artifacttest.AABWith(t, t.TempDir(), "app.aab", "com.example.app",
		artifacttest.FillerMembers(artifact.MaxZipEntries+1))
	rt := &uploadRT{
		t:                  t,
		editID:             "edit-huge",
		versionCode:        9,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"9","status":"completed","versionCodes":["9"]}]}`,
	}
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", AABPath: p}); err != nil {
		t.Fatalf("Run: %v, want an over-cap bundle to upload rather than be refused", err)
	}
	if !strings.Contains(stderr.String(), "not classified") {
		t.Errorf("stderr %q should say the container went unclassified", stderr.String())
	}
}

// TestRun_skipPreflight_stillRefusesAMissingArtifact pins that the escape
// hatch is not a way around the local-file check. This surface has its own
// stat in the orchestrator's dry-run, so it never lost the refusal the way
// `customapps create` and `releases expansion-files upload` did; the test is
// here so the three surfaces answer alike whichever check gets there first.
func TestRun_skipPreflight_stillRefusesAMissingArtifact(t *testing.T) {
	rt := &uploadRT{t: t}
	rc, _ := newRC(t, rt)

	_, err := upload.Run(rc, upload.Input{
		Package:       "com.example.app",
		Track:         "internal",
		AABPath:       filepath.Join(t.TempDir(), "never-built.aab"),
		SkipPreflight: true,
		DryRun:        true,
	})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("a missing artifact must make no network call; calls=%v", rt.calls)
	}
}
