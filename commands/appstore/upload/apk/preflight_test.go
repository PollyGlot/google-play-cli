package apk_test

// Artifact preflight (PRD #448) on `gplay appstore upload apk`. The hosted
// app is addressed by package, so both checks apply here; the refusal lands
// before the resumable session is reserved, which on a 10 GiB APK is the
// whole point.

import (
	"strings"
	"testing"

	apkcmd "github.com/PollyGlot/google-play-cli/commands/appstore/upload/apk"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestRun_preflight_refusesANonAPKContainer(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	p := artifacttest.AAB(t, t.TempDir(), "app.apk", "com.example.app")
	_, err := apkcmd.Run(rc, apkcmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: p})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	// The /token exchange may have happened (auth precedes the upload), but no
	// upload session must have been reserved.
	for _, c := range rt.calls {
		if strings.Contains(c, "apks:upload") {
			t.Errorf("a refused artifact reserved an upload session; calls=%v", rt.calls)
		}
	}
	if !strings.Contains(err.Error(), "expected an APK") {
		t.Errorf("refusal %q does not name what was expected", err)
	}
}

func TestRun_preflight_refusesAnotherAppsBuild(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	p := artifacttest.APK(t, t.TempDir(), "app.apk", "com.other.app")
	_, err := apkcmd.Run(rc, apkcmd.Input{StorePackage: "com.example.store", Package: "com.example.app", Path: p})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "package mismatch") {
		t.Errorf("refusal %q does not name the mismatch", err)
	}
}

func TestRun_skipPreflight_restoresPreCheckBehaviour(t *testing.T) {
	rt := &testRoundTripper{}
	rc := newRC(t, rt)

	p := artifacttest.WriteFile(t, t.TempDir(), "app.apk", []byte("not an artifact at all"))
	if _, err := apkcmd.Run(rc, apkcmd.Input{
		StorePackage: "com.example.store", Package: "com.example.app", Path: p, SkipPreflight: true,
	}); err != nil {
		t.Fatalf("Run with --skip-preflight: %v", err)
	}
	if !strings.Contains(rt.initURL, "apks:upload") {
		t.Errorf("--skip-preflight must let the upload proceed; url=%q", rt.initURL)
	}
}
