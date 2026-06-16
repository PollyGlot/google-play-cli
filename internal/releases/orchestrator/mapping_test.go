// Package orchestrator_test — UploadMapping exercises the standalone
// mapping-upload choreography behind `gplay releases mappings upload`:
// open an Edit, POST the deobfuscation file keyed by an explicit
// versionCode, and commit — no track update. Reuses playRT and the
// helpers from orchestrator_test.go (same package).
package orchestrator_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// TestUploadMapping_happyPath_beginUploadCommit asserts the standalone
// flow opens its own Edit, uploads the mapping keyed by the explicit
// versionCode + proguard type, and commits — a one-shot begin → upload →
// commit, no track update (#250).
func TestUploadMapping_happyPath_beginUploadCommit(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &playRT{t: t, editID: "edit-map"}
	hc := &http.Client{Transport: rt}

	res, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: mapping,
	})
	if err != nil {
		t.Fatalf("UploadMapping: %v", err)
	}

	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-map/apks/142/deobfuscationFiles/proguard",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-map:commit",
	}
	if len(rt.calls) != len(wantPaths) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantPaths))
	}
	for i, want := range wantPaths {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}
	if res.VersionCode != 142 {
		t.Errorf("VersionCode = %d, want 142", res.VersionCode)
	}
	if res.FileType != "proguard" {
		t.Errorf("FileType = %q, want proguard", res.FileType)
	}
	if res.SymbolType != "proguard" {
		t.Errorf("SymbolType = %q, want proguard (from the response)", res.SymbolType)
	}
	if len(rt.deobfReqBody) == 0 {
		t.Error("deobfuscationfiles.upload received an empty body")
	}
}

// TestUploadMapping_nativeCodeType_inPath asserts an explicit nativeCode
// type lands verbatim in the path (Discovery enum, not invented).
func TestUploadMapping_nativeCodeType_inPath(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &playRT{t: t, editID: "edit-map"}
	hc := &http.Client{Transport: rt}

	if _, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 7,
		MappingPath: mapping,
		FileType:    "nativeCode",
	}); err != nil {
		t.Fatalf("UploadMapping: %v", err)
	}
	found := false
	for _, c := range rt.calls {
		if strings.HasSuffix(c, "/apks/7/deobfuscationFiles/nativeCode") {
			found = true
		}
	}
	if !found {
		t.Errorf("no deobfuscationFiles/nativeCode call in %v", rt.calls)
	}
}

// TestUploadMapping_invalidVersionCode_exit2_noHTTP asserts a non-positive
// versionCode is a caller-side misuse (exit 2) caught before any HTTP.
func TestUploadMapping_invalidVersionCode_exit2_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &playRT{t: t}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 0,
		MappingPath: mapping,
	})
	if err == nil {
		t.Fatal("UploadMapping returned nil error for versionCode 0")
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("hit the network on an invalid versionCode: %v", rt.calls)
	}
}

// TestUploadMapping_invalidType_exit2_noHTTP asserts a file type outside
// the Discovery enum (proguard / nativeCode) is rejected before any HTTP.
func TestUploadMapping_invalidType_exit2_noHTTP(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &playRT{t: t}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: mapping,
		FileType:    "deobfuscationFileTypeUnspecified",
	})
	if err == nil {
		t.Fatal("UploadMapping accepted an out-of-enum file type")
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("hit the network on an invalid file type: %v", rt.calls)
	}
}

// TestUploadMapping_dryRun_validatesFile_noHTTP asserts dry-run validates
// the mapping is readable (exit 20 when missing) and performs no HTTP.
func TestUploadMapping_dryRun_validatesFile_noHTTP(t *testing.T) {
	// Missing file → exit 20, no HTTP.
	rt := &playRT{t: t}
	hc := &http.Client{Transport: rt}
	_, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: "/no/such/mapping.txt",
		DryRun:      true,
	})
	if err == nil {
		t.Fatal("dry-run returned nil error for a missing mapping")
	}
	if got := exitCode(err); got != 20 {
		t.Errorf("exit code = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run hit the network: %v", rt.calls)
	}

	// Present file → succeeds, no HTTP.
	mapping := writeFakeMapping(t)
	rt2 := &playRT{t: t}
	hc2 := &http.Client{Transport: rt2}
	if _, err := orchestrator.UploadMapping(context.Background(), hc2, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: mapping,
		DryRun:      true,
	}); err != nil {
		t.Fatalf("dry-run with a present mapping: %v", err)
	}
	if len(rt2.calls) != 0 {
		t.Errorf("dry-run hit the network: %v", rt2.calls)
	}
}

// TestUploadMapping_uploadFails_discardsEdit asserts a failed
// deobfuscation upload auto-discards the open Edit (edits.delete) so no
// 24h lock leaks — the implicit-edit cleanup contract (DESIGN §4).
func TestUploadMapping_uploadFails_discardsEdit(t *testing.T) {
	mapping := writeFakeMapping(t)
	rt := &playRT{
		t:      t,
		editID: "edit-map",
		deobfHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(500, `{"error":{"code":500,"message":"boom"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

	if _, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: mapping,
	}); err == nil {
		t.Fatal("UploadMapping returned nil error on a 500 upload")
	}
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("no edits.delete after a failed upload; calls=%v", rt.calls)
	}
}

// TestUploadMapping_dryRun_directoryPath_exit20_noHTTP asserts dry-run
// rejects a non-regular mapping path (a directory) as exit 20 — parity
// with the live path, which cannot stream a directory (PR #264 review).
func TestUploadMapping_dryRun_directoryPath_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir()
	rt := &playRT{t: t}
	hc := &http.Client{Transport: rt}
	_, err := orchestrator.UploadMapping(context.Background(), hc, orchestrator.MappingOpts{
		Package:     "com.example.app",
		VersionCode: 142,
		MappingPath: dir,
		DryRun:      true,
	})
	if err == nil {
		t.Fatal("dry-run accepted a directory mapping path")
	}
	if got := exitCode(err); got != 20 {
		t.Errorf("exit code = %d, want 20; err=%v", got, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run hit the network: %v", rt.calls)
	}
}
