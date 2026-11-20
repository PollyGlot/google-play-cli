package upload_test

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

func TestRun_preflight_refusesAPKPassedToABundleUpload(t *testing.T) {
	p := artifacttest.APK(t, t.TempDir(), "app.aab", "com.example.app")
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{p}})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a refused artifact must make no network call; calls=%v", apiCalls(rt))
	}
	for _, want := range []string{"expected an Android App Bundle (AAB)", "found an APK", "--skip-preflight"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestRun_preflight_refusesAnotherAppsBuild(t *testing.T) {
	p := artifacttest.AAB(t, t.TempDir(), "app.aab", "com.other.app")
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{p}})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a refused artifact must make no network call; calls=%v", apiCalls(rt))
	}
	for _, want := range []string{"package mismatch", `"com.example.app"`, `"com.other.app"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestRun_skipPreflight_restoresPreCheckBehaviour(t *testing.T) {
	p := artifacttest.WriteFile(t, t.TempDir(), "app.aab", []byte("not an artifact at all"))
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-skip",
		versionCode:        7,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"7","status":"completed","versionCodes":["7"]}]}`,
	})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:       "com.example.app",
		Track:         "internal",
		Artifacts:     []string{p},
		SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-skip/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-skip/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-skip/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-skip:commit",
	}
	assertSequence(t, rt, wantSequence)
}

func TestRun_preflight_dryRunReportsWhatItFound(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeFakeAAB(t)},
		DryRun:    true,
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run must make no network call; calls=%v", apiCalls(rt))
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

func TestRun_preflight_unreadableManifestDegradesRatherThanRefuses(t *testing.T) {
	p := artifacttest.Zip(t, t.TempDir(), "app.aab", map[string][]byte{
		"BundleConfig.pb":                   {},
		"base/manifest/AndroidManifest.xml": []byte("\xff\xff not a protobuf manifest"),
	})
	_, transport := newUploadTransport(uploadAPI{
		editID:             "edit-degraded",
		versionCode:        8,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"8","status":"completed","versionCodes":["8"]}]}`,
	})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{p}}); err != nil {
		t.Fatalf("Run: %v, want the upload to proceed on a degraded parse", err)
	}
	if !strings.Contains(stderr.String(), "preflight") {
		t.Errorf("stderr %q does not report the degraded parse", stderr.String())
	}
}

func TestRun_preflight_anAssetRichBundleOverTheMemberCapStillUploads(t *testing.T) {
	p := artifacttest.AABWith(t, t.TempDir(), "app.aab", "com.example.app",
		artifacttest.FillerMembers(artifact.MaxZipEntries+1))
	_, transport := newUploadTransport(uploadAPI{
		editID:             "edit-huge",
		versionCode:        9,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"9","status":"completed","versionCodes":["9"]}]}`,
	})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{p}}); err != nil {
		t.Fatalf("Run: %v, want an over-cap bundle to upload rather than be refused", err)
	}
	if !strings.Contains(stderr.String(), "not classified") {
		t.Errorf("stderr %q should say the container went unclassified", stderr.String())
	}
}

func TestRun_skipPreflight_stillRefusesAMissingArtifact(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:       "com.example.app",
		Track:         "internal",
		Artifacts:     []string{filepath.Join(t.TempDir(), "never-built.aab")},
		SkipPreflight: true,
		DryRun:        true,
	})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a missing artifact must make no network call; calls=%v", apiCalls(rt))
	}
}
