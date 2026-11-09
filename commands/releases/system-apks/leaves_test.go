// Behavior of the four `gplay releases system-apks` leaves, driven through
// their Run functions against the offline Play fake.
package systemapks_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/create"
	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/download"
	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/list"
	"github.com/PollyGlot/google-play-cli/commands/releases/system-apks/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const (
	pkg         = "com.example.app"
	variantsURL = "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/systemApks/1042/variants"
	variantBody = `{"variantId":3,"deviceSpec":{"screenDensity":480,"supportedAbis":["arm64-v8a","armeabi-v7a"],"supportedLocales":["en-US"]},"options":{"rotated":true}}`
	apkBytes    = "PK\x03\x04system-apk-payload"
)

func newRC(t *testing.T, rt http.RoundTripper, format output.Format) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout, Stderr: &stderr}, kernel.Inputs{Format: format})
	rc.Account = sa
	return rc, &stdout, &stderr
}

func render(t *testing.T, r output.Renderable, json bool) string {
	t.Helper()
	var buf bytes.Buffer
	fn := r.Renderers().Table
	if json {
		fn = r.Renderers().JSON
	}
	if err := fn(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != want {
		t.Fatalf("err = %v, want exit %d", err, want)
	}
}

func assertOffline(t *testing.T, fake *testkit.Fake) {
	t.Helper()
	if n := len(fake.Calls()) + fake.TokenExchanges(); n != 0 {
		t.Errorf("must not reach the network; calls=%v tokens=%d", fake.Calls(), fake.TokenExchanges())
	}
}

func TestCreate_sendsTheDeviceSpecAndPassesTheVariantThrough(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
	rc, _, stderr := newRC(t, fake, output.FormatJSON)
	r, err := create.Run(rc, create.Input{
		Package: pkg, VersionCode: 1042,
		ABIs: []string{"arm64-v8a", "armeabi-v7a"}, ScreenDensity: 480, Locales: []string{"en-US"},
		RotatedKey: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].URL != variantsURL {
		t.Fatalf("calls = %+v, want one POST %s", calls, variantsURL)
	}
	wantBody := `{"deviceSpec":{"screenDensity":480,"supportedAbis":["arm64-v8a","armeabi-v7a"],"supportedLocales":["en-US"]},"options":{"rotated":true}}`
	if got := strings.TrimSpace(string(calls[0].Body)); got != wantBody {
		t.Errorf("body = %s, want %s", got, wantBody)
	}
	if got := render(t, r, true); got != variantBody {
		t.Errorf("json = %s, want the verbatim Variant", got)
	}
	if !strings.Contains(stderr.String(), "created system APK variant 3") {
		t.Errorf("stderr %q should confirm the created variantId", stderr.String())
	}
}

func TestCreate_dryRunPrintsTheRequestOffline(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
	rc, _, _ := newRC(t, fake, output.FormatJSON)
	r, err := create.Run(rc, create.Input{Package: pkg, VersionCode: 1042, ABIs: []string{"x86_64"}, UncompressedDex: true, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOffline(t, fake)
	got := render(t, r, true)
	for _, want := range []string{`"dryRun": true`, `"versionCode": 1042`, `"supportedAbis": [`, `"x86_64"`, `"uncompressedDexFiles": true`, `"requires": []`} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run json %s lacks %s", got, want)
		}
	}
	if table := render(t, r, false); !strings.Contains(table, "would create a system APK variant") || !strings.Contains(table, "uncompressed-dex") {
		t.Errorf("dry-run table %q should announce the rehearsal and the options", table)
	}
}

func TestCreate_refusesBadInputOffline(t *testing.T) {
	for name, in := range map[string]create.Input{
		"no version code":  {Package: pkg},
		"negative density": {Package: pkg, VersionCode: 1042, ScreenDensity: -1},
	} {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
			rc, _, _ := newRC(t, fake, output.FormatJSON)
			_, err := create.Run(rc, in)
			assertExit(t, err, 2)
			assertOffline(t, fake)
		})
	}
}

func TestList_rendersOneRowPerVariant(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, `{"variants":[`+variantBody+`]}`))
	rc, _, _ := newRC(t, fake, output.FormatTable)
	r, err := list.Run(rc, list.Input{Package: pkg, VersionCode: 1042})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].URL != variantsURL {
		t.Fatalf("calls = %+v, want one GET %s", calls, variantsURL)
	}
	table := render(t, r, false)
	for _, want := range []string{"VARIANT-ID", "3", "480", "arm64-v8a,armeabi-v7a", "en-US", "rotated-key"} {
		if !strings.Contains(table, want) {
			t.Errorf("table %q lacks %q", table, want)
		}
	}
}

func TestView_readsOneVariant(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
	rc, _, _ := newRC(t, fake, output.FormatJSON)
	r, err := view.Run(rc, view.Input{Package: pkg, VersionCode: 1042, VariantID: "3"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].URL != variantsURL+"/3" {
		t.Fatalf("calls = %+v, want one GET %s/3", calls, variantsURL)
	}
	if got := render(t, r, true); got != variantBody {
		t.Errorf("json = %s, want the verbatim Variant", got)
	}
}

func TestView_404CarriesAHintAndExit30(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusNotFound, `{"error":{"message":"not found"}}`))
	rc, _, _ := newRC(t, fake, output.FormatJSON)
	_, err := view.Run(rc, view.Input{Package: pkg, VersionCode: 1042, VariantID: "9"})
	assertExit(t, err, 30)
	if !strings.Contains(err.Error(), "gplay releases system-apks list") {
		t.Errorf("err %q should point at the list command", err)
	}
}

func TestVariantID_mustBeAnUnsignedInteger(t *testing.T) {
	for _, id := range []string{"", "abc", "-1", "4294967296"} {
		t.Run(id, func(t *testing.T) {
			fake := testkit.NewFake(testkit.Any(http.StatusOK, variantBody))
			rc, _, _ := newRC(t, fake, output.FormatJSON)
			_, err := view.Run(rc, view.Input{Package: pkg, VersionCode: 1042, VariantID: id})
			assertExit(t, err, 2)
			err = download.Run(rc, download.Input{Package: pkg, VersionCode: 1042, VariantID: id, Dest: "-"})
			assertExit(t, err, 2)
			assertOffline(t, fake)
		})
	}
}

// binaryRT answers the alt=media GET with raw bytes, which the Fake's JSON
// responses cannot express.
func binaryRT(status int, gotURL *string) http.RoundTripper {
	return testkit.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if resp, ok := testkit.TokenResponse(req); ok {
			return resp, nil
		}
		*gotURL = req.URL.String()
		if status != http.StatusOK {
			return testkit.Response(status, `{"error":{"message":"nope"}}`), nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(apkBytes))}, nil
	})
}

func TestDownload_writesTheBytesToDest(t *testing.T) {
	var gotURL string
	rc, stdout, stderr := newRC(t, binaryRT(http.StatusOK, &gotURL), output.FormatTable)
	dest := filepath.Join(t.TempDir(), "system.apk")
	if err := download.Run(rc, download.Input{Package: pkg, VersionCode: 1042, VariantID: "3", Dest: dest}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotURL != variantsURL+"/3:download?alt=media" {
		t.Errorf("url = %q, want the :download alt=media endpoint", gotURL)
	}
	if got, _ := os.ReadFile(dest); string(got) != apkBytes {
		t.Errorf("file = %q, want %q", got, apkBytes)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay empty for a file download, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "✓") || !strings.Contains(stderr.String(), dest) {
		t.Errorf("stderr %q should confirm the destination", stderr.String())
	}
}

func TestDownload_destDashStreamsToStdout(t *testing.T) {
	var gotURL string
	rc, stdout, _ := newRC(t, binaryRT(http.StatusOK, &gotURL), output.FormatTable)
	if err := download.Run(rc, download.Input{Package: pkg, VersionCode: 1042, VariantID: "3", Dest: "-"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.String() != apkBytes {
		t.Errorf("stdout = %q, want the raw bytes", stdout.String())
	}
}

func TestDownload_failureLeavesNoFile(t *testing.T) {
	var gotURL string
	rc, _, _ := newRC(t, binaryRT(http.StatusNotFound, &gotURL), output.FormatTable)
	dest := filepath.Join(t.TempDir(), "system.apk")
	err := download.Run(rc, download.Input{Package: pkg, VersionCode: 1042, VariantID: "3", Dest: dest})
	assertExit(t, err, 30)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("a failed download left %s behind", dest)
	}
}
