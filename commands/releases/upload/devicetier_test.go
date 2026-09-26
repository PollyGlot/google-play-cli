package upload_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// TestRun_deviceTierConfig_forwardedToBundlesUpload asserts
// --device-tier-config reaches edits.bundles.upload as deviceTierConfigId and
// leaves the rest of the release unchanged (#603).
func TestRun_deviceTierConfig_forwardedToBundlesUpload(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-dtc",
		versionCode:        7,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"7","status":"completed","versionCodes":["7"]}]}`,
	})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:          "com.example.app",
		Track:            "internal",
		AABPath:          writeFakeAAB(t),
		DeviceTierConfig: "1234567890",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	query := bundlesInitQuery(rt)
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse bundles initiate query %q: %v", query, err)
	}
	if got := q.Get("deviceTierConfigId"); got != "1234567890" {
		t.Errorf("deviceTierConfigId = %q, want 1234567890 (query %q)", got, query)
	}
}

// TestRun_deviceTierConfig_withAPK_exit2_noHTTP asserts the flag is refused
// for an APK, which has no device tier parameter, before any request: a
// silently dropped flag would ship a release the operator did not ask for.
func TestRun_deviceTierConfig_withAPK_exit2_noHTTP(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:          "com.example.app",
		Track:            "internal",
		AABPath:          writeFakeAPK(t),
		DeviceTierConfig: "LATEST",
	})
	if err == nil {
		t.Fatal("Run accepted --device-tier-config for an APK")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "--device-tier-config") {
		t.Errorf("error %q does not name the flag", err)
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(apiCalls(rt)), apiCalls(rt))
	}
}

// TestNewCommand_deviceTierConfig_documented asserts the flag is registered
// and explained in the command's help.
func TestNewCommand_deviceTierConfig_documented(t *testing.T) {
	cmd := upload.NewCommand(kernel.Boot{})
	if cmd.Flags().Lookup("device-tier-config") == nil {
		t.Fatal("releases upload has no --device-tier-config flag")
	}
	if !strings.Contains(cmd.Long, "--device-tier-config") {
		t.Error("releases upload --help does not explain --device-tier-config")
	}
}
