package upload_test

// Externally hosted APKs (#546): --externally-hosted registers an
// ExternallyHostedApk resource through edits.apks.addexternallyhosted and
// joins its versionCode to the same release as the uploaded artifacts.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// writeHostedFile writes body as an externally hosted APK resource file.
func writeHostedFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ext.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

const hostedResource = `{"packageName":"com.example.app","externallyHostedUrl":"https://cdn.example.com/app-310.apk","versionCode":310}`

// TestRun_externallyHostedAlone_insertAddTrackCommit asserts the flag alone
// is a complete release: edits.insert → apks.addexternallyhosted →
// tracks.update → edits.commit, the release holding the returned code.
func TestRun_externallyHostedAlone_insertAddTrackCommit(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-h", hostedVersionCode: 310})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package:          "com.example.app",
		Track:            "internal",
		ExternallyHosted: writeHostedFile(t, hostedResource),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertSequence(t, rt, []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-h/apks/externallyHosted",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-h/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-h:commit",
	})
	if name, codes := trackRelease(t, rt); name != "310" || strings.Join(codes, ",") != "310" {
		t.Errorf("release = (%q, %v), want (310, [310])", name, codes)
	}
	if !strings.Contains(stderr.String(), "uploaded versionCode 310 to track") {
		t.Errorf("✓ line does not carry the returned code; stderr=%q", stderr.String())
	}
}

// TestRun_externallyHostedWithAAB_releaseHoldsBothCodes asserts the flag
// combines with an uploaded AAB and --retain-version-code: one Edit, one
// tracks.update listing the uploaded, hosted, then retained codes.
func TestRun_externallyHostedWithAAB_releaseHoldsBothCodes(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-mix", versionCode: 311, hostedVersionCode: 310})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:            "com.example.app",
		Track:              "internal",
		Artifacts:          []string{writeNamedAAB(t, "phone.aab")},
		ExternallyHosted:   writeHostedFile(t, hostedResource),
		RetainVersionCodes: []int{41},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertSequence(t, rt, []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-mix/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-mix/bundles",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-mix/apks/externallyHosted",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-mix/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-mix:commit",
	})
	if name, codes := trackRelease(t, rt); name != "311" || strings.Join(codes, ",") != "311,310,41" {
		t.Errorf("release = (%q, %v), want (311, [311 310 41])", name, codes)
	}
}

// TestRun_externallyHosted_badResource_exit20_noNetwork asserts malformed
// JSON and a package mismatch fail offline, before edits.insert and before
// the token exchange, on the live path and on --dry-run.
func TestRun_externallyHosted_badResource_exit20_noNetwork(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":   `{"packageName":`,
		"package mismatch": `{"packageName":"com.other.app"}`,
	} {
		for _, dryRun := range []bool{false, true} {
			rt, transport := newUploadTransport(uploadAPI{editID: "edit-never"})
			rc, _ := newRC(t, transport)
			_, err := upload.Run(rc, upload.Input{
				Package:          "com.example.app",
				Track:            "internal",
				ExternallyHosted: writeHostedFile(t, body),
				DryRun:           dryRun,
			})
			if got := exit.For(err); got != 20 {
				t.Errorf("%s (dryRun=%v): exit = %d, want 20; err=%v", name, dryRun, got, err)
			}
			if touched(rt) {
				t.Errorf("%s (dryRun=%v): a bad resource must make no network call; calls=%v", name, dryRun, apiCalls(rt))
			}
		}
	}
}

// TestRun_externallyHosted_usageRefusals_exit2 pins the flag combinations
// refused as CLI misuse, before any network call.
func TestRun_externallyHosted_usageRefusals_exit2(t *testing.T) {
	cases := map[string]upload.Input{
		"blank path":                 {ExternallyHosted: "  "},
		"--format without an upload": {Format: "apk"},
		"--mapping with an AAB too":  {Artifacts: []string{"phone.aab"}, Mapping: "mapping.txt"},
		"--device-tier-config alone": {DeviceTierConfig: "LATEST"},
	}
	for name, in := range cases {
		rt, transport := newUploadTransport(uploadAPI{})
		rc, _ := newRC(t, transport)
		in.Package, in.Track = "com.example.app", "internal"
		if in.ExternallyHosted == "" {
			in.ExternallyHosted = writeHostedFile(t, hostedResource)
		}
		for i, a := range in.Artifacts {
			in.Artifacts[i] = writeNamedAAB(t, a)
		}
		_, err := upload.Run(rc, in)
		if got := exit.For(err); got != 2 {
			t.Errorf("%s: exit = %d, want 2; err=%v", name, got, err)
		}
		if touched(rt) {
			t.Errorf("%s: a usage error must make no network call; calls=%v", name, apiCalls(rt))
		}
	}
}

// TestRun_externallyHosted_dryRun_previewWithoutNetwork asserts --dry-run
// with the flag alone previews the release with nothing on the wire.
func TestRun_externallyHosted_dryRun_previewWithoutNetwork(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package:          "com.example.app",
		Track:            "internal",
		ExternallyHosted: writeHostedFile(t, hostedResource),
		DryRun:           true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run made network calls: %v", apiCalls(rt))
	}
	got := string(outputtest.RenderJSON(t, r))
	if !strings.Contains(got, `"dryRun": true`) || !strings.Contains(got, `"track": "internal"`) {
		t.Errorf("dry-run JSON = %s, want the dryRun marker and the track", got)
	}
}

// TestNewCommand_externallyHostedFlag_experimentalAndStandsInForArtifact
// asserts the flag is labelled experimental on the frozen command, and that
// it alone satisfies the positional-artifact check while a bare invocation
// keeps the historical "missing <artifact>" refusal.
func TestNewCommand_externallyHostedFlag_experimentalAndStandsInForArtifact(t *testing.T) {
	cmd := upload.NewCommand(kernel.Boot{})
	f := cmd.Flags().Lookup("externally-hosted")
	if f == nil {
		t.Fatal("--externally-hosted is not registered")
	}
	if !kernel.IsExperimentalFlag(cmd, f) || kernel.IsExperimental(cmd) {
		t.Errorf("stability = (flag %v, command %v), want (experimental, frozen)", kernel.IsExperimentalFlag(cmd, f), kernel.IsExperimental(cmd))
	}
	for _, want := range []string{"Managed Play", "private app", "gplay customapps", "gplay schema ExternallyHostedApk"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("--help does not say %q", want)
		}
	}

	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("Args accepted a bare invocation, want the missing-artifact refusal")
	}
	if err := cmd.Flags().Set("externally-hosted", "ext.json"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Args(cmd, nil); err != nil {
		t.Errorf("Args with --externally-hosted and no artifact = %v, want nil", err)
	}
}
