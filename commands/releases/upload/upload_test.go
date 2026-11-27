package upload_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type uploadAPI struct {
	editID             string
	versionCode        int
	trackUpdateRawResp string
	trackUpdateStatus  int
	versionCodes       []int
	failArtifact       int
	hostedVersionCode  int
}

func newUploadTransport(a uploadAPI) (*testkit.Fake, http.RoundTripper) {
	var (
		mu                 sync.Mutex
		initiates, uploads int
	)
	uploaded := func() string {
		mu.Lock()
		defer mu.Unlock()
		code := a.versionCode
		if uploads < len(a.versionCodes) {
			code = a.versionCodes[uploads]
		}
		uploads++
		return fmt.Sprintf(`{"versionCode":%d,"sha1":"abc","sha256":"def"}`, code)
	}
	refuseInitiate := func(c testkit.Call) bool {
		if strings.Contains(c.Path, "/deobfuscationFiles/") {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		initiates++
		return initiates == a.failArtifact
	}
	f := testkit.NewFake(testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case isResumableInitiate(c.Method, c.Path):
			if refuseInitiate(c) {
				return http.StatusBadRequest, `{"error":{"code":400,"message":"Version code already used."}}`, true
			}
			return http.StatusOK, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/deobfuscationFiles/"):
			return http.StatusOK, `{"deobfuscationFile":{"symbolType":"proguard"}}`, true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/apks/externallyHosted"):
			return http.StatusOK, fmt.Sprintf(`{"externallyHostedApk":{"packageName":"com.example.app","versionCode":%d}}`, a.hostedVersionCode), true
		case c.Method == http.MethodPut && (strings.HasSuffix(c.Path, "/bundles") || strings.HasSuffix(c.Path, "/apks")):
			return http.StatusOK, uploaded(), true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/"):
			if a.trackUpdateStatus >= 400 {
				return a.trackUpdateStatus, `{"error":{"code":404,"message":"Track not found."}}`, true
			}
			if a.trackUpdateRawResp == "" {
				return http.StatusOK, `{}`, true
			}
			return http.StatusOK, a.trackUpdateRawResp, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, a.editID), true
		}
		return 0, "", false
	}, "Location", func(c testkit.Call, status int) string {
		if status == http.StatusOK && isResumableInitiate(c.Method, c.Path) {
			return "https://" + c.Host + c.Path + "?upload_id=session-" + a.editID
		}
		return ""
	}))
	return f, f
}

func isResumableInitiate(method, path string) bool {
	return method == http.MethodPost && (strings.HasSuffix(path, "/bundles") ||
		strings.HasSuffix(path, "/apks") || strings.Contains(path, "/deobfuscationFiles/"))
}

func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func assertSequence(t *testing.T, f *testkit.Fake, wantSequence []string) {
	t.Helper()
	calls := apiCalls(f)
	if len(calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(calls), calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
	}
	if n := f.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1", n)
	}
}

func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

func trackUpdateReq(f *testkit.Fake) []byte {
	var body []byte
	for _, c := range f.Calls() {
		if c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/") {
			body = c.Body
		}
	}
	return body
}

func bundlesInitQuery(f *testkit.Fake) string {
	for _, c := range f.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/bundles") {
			return c.Query
		}
	}
	return ""
}

func writeFakeAAB(t *testing.T) string {
	t.Helper()
	return artifacttest.AAB(t, t.TempDir(), "app.aab", "com.example.app")
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

func TestRun_internalTrack_happyPath_hitsTokenAndAndroidPublisher(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{aab},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if rt.TokenExchanges() == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", apiCalls(rt))
	}
	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	assertSequence(t, rt, wantSequence)
}

func TestRun_mutualExclusion_releaseNotesAndDir_exit2_noHTTP(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:         "com.example.app",
		Track:           "internal",
		Artifacts:       []string{writeFakeAAB(t)},
		ReleaseNotes:    "hello",
		ReleaseNotesDir: "/tmp/notes",
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(apiCalls(rt)), apiCalls(rt))
	}
}

func TestRun_stagedOne_exit2PointingAtComplete_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:           "com.example.app",
		Track:             "production",
		Artifacts:         []string{aab},
		StagedFraction:    1.0,
		StagedFractionSet: true,
		Confirm:           true,
	})
	if got := exit.For(err); got != 2 {
		t.Fatalf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "--complete") {
		t.Errorf("err = %q, want it to point at --complete", err.Error())
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(apiCalls(rt)), apiCalls(rt))
	}
}

func TestRun_productionPublishWithoutConfirm_exit3_noHTTP(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []string{aab},
		Complete:  true,
		Confirm:   false,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want a --confirm safety refusal")
	}
	if got := exit.For(err); got != 3 {
		t.Errorf("exit.For(err) = %d, want 3; err=%v", got, err)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw %d calls on confirm-guarded error: %v", len(apiCalls(rt)), apiCalls(rt))
	}
}

func TestRun_productionPublishWithConfirm_succeedsAndHitsAPI(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-prod",
		versionCode:        200,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"completed","versionCodes":["200"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "production",
		Artifacts: []string{aab},
		Complete:  true,
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on confirmed production publish")
	}
	if rt.TokenExchanges() == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", apiCalls(rt))
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"track":"production"`) {
		t.Errorf("tracks.update body = %s, want track=production", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("tracks.update body = %s, want no userFraction on a completed release", body)
	}
}

func TestRun_uploadToMissingClosedTrack_hintsTracksCreate(t *testing.T) {
	aab := writeFakeAAB(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:            "edit-miss",
		versionCode:       142,
		trackUpdateStatus: http.StatusNotFound,
	})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Artifacts: []string{aab},
	})
	if err == nil {
		t.Fatal("Run returned nil error; want a track-not-found hint")
	}
	if !strings.Contains(err.Error(), "gplay tracks create qa-team") {
		t.Errorf("error %q is missing the `gplay tracks create qa-team` hint", err.Error())
	}
	if code := exit.For(err); code != 30 {
		t.Errorf("exit.For(err) = %d, want 30 (underlying *api.Error preserved); err=%v", code, err)
	}
	for _, c := range apiCalls(rt) {
		if c == "POST /androidpublisher/v3/applications/com.example.app/edits/edit-miss/tracks" {
			t.Errorf("upload auto-created a track (saw %q); it must only PUT tracks.update", c)
		}
	}
}

func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := upload.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"track",
		"release-notes",
		"release-notes-dir",
		"draft",
		"complete",
		"staged",
		"keep-edit-on-failure",
		"confirm",
		"dry-run",
		"mapping",
		"format",
		"retain-version-code",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "upload <artifact>..." {
		t.Errorf("cmd.Use = %q, want %q", got, "upload <artifact>...")
	}
}

func TestRun_happyPath_emitsConfirmationOnStderr(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newUploadTransport(uploadAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{aab}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("stderr missing ✓ confirmation line:\n%s", got)
	}
	for _, want := range []string{"142", "internal", "completed"} {
		if !strings.Contains(got, want) {
			t.Errorf("✓ line %q missing %q", got, want)
		}
	}
}

func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{aab}, DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}

func TestRun_inProgressRollout_confirmationShowsUserFractionPercent(t *testing.T) {
	aab := writeFakeAAB(t)
	_, transport := newUploadTransport(uploadAPI{
		editID:             "edit-staged",
		versionCode:        77,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"77","status":"inProgress","versionCodes":["77"],"userFraction":0.05}]}`,
	})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{Package: "com.example.app", Track: "beta", Artifacts: []string{aab}, StagedFraction: 0.05, StagedFractionSet: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "5%") {
		t.Errorf("✓ line should render userFraction 0.05 as 5%%; stderr=%q", stderr.String())
	}
}

func writeFakeMapping(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mapping.txt")
	if err := os.WriteFile(p, []byte("com.example.Foo -> a.a:\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestRun_withMapping_uploadsMappingInSameEditAndConfirms(t *testing.T) {
	aab := writeFakeAAB(t)
	mapping := writeFakeMapping(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-xyz",
		versionCode:        142,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{aab},
		Mapping:   mapping,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/bundles",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-xyz/apks/142/deobfuscationFiles/proguard",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-xyz/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-xyz:commit",
	}
	assertSequence(t, rt, wantSequence)
	if !strings.Contains(stderr.String(), "mapping") {
		t.Errorf("✓ line should mention the uploaded mapping; stderr=%q", stderr.String())
	}
}

func writeFakeAPK(t *testing.T) string {
	t.Helper()
	return artifacttest.APK(t, t.TempDir(), "app.apk", "com.example.app")
}

func TestRun_apkExtension_ridesApksUploadEndpoint(t *testing.T) {
	apk := writeFakeAPK(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-apk",
		versionCode:        91,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"91","status":"completed","versionCodes":["91"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{apk},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-apk/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-apk:commit",
	}
	assertSequence(t, rt, wantSequence)
}

func TestRun_formatApkOverride_forcesApkPathForNonApkFilename(t *testing.T) {
	p := artifacttest.APK(t, t.TempDir(), "build.bin", "com.example.app")
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-fmt",
		versionCode:        5,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"5","status":"completed","versionCodes":["5"]}]}`,
	})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{p},
		Format:    "apk",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sawApks := false
	for _, c := range apiCalls(rt) {
		if strings.HasSuffix(c, "/apks") {
			sawApks = true
		}
		if strings.HasSuffix(c, "/bundles") {
			t.Errorf("--format apk still hit the bundles endpoint: %q", c)
		}
	}
	if !sawApks {
		t.Errorf("--format apk did not hit the apks endpoint; calls=%v", apiCalls(rt))
	}
}

func TestRun_unknownExtension_noFormat_exit2_noHTTP(t *testing.T) {
	p := artifacttest.WriteFile(t, t.TempDir(), "build.bin", []byte("x"))
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{p},
	})
	if err == nil {
		t.Fatal("Run accepted an unknown-extension artifact without --format")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "cannot tell APK from AAB by extension") {
		t.Errorf("error %q is missing the sharing-parity extension message", err.Error())
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw %d calls on a usage error: %v", len(apiCalls(rt)), apiCalls(rt))
	}
}

func TestRun_apkWithMapping_uploadsMappingAgainstApkVersionCode(t *testing.T) {
	apk := writeFakeAPK(t)
	mapping := writeFakeMapping(t)
	rt, transport := newUploadTransport(uploadAPI{
		editID:             "edit-apk",
		versionCode:        91,
		trackUpdateRawResp: `{"track":"internal","releases":[{"name":"91","status":"completed","versionCodes":["91"]}]}`,
	})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{apk},
		Mapping:   mapping,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks/91/deobfuscationFiles/proguard",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-apk/apks/91/deobfuscationFiles/proguard",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-apk/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-apk:commit",
	}
	assertSequence(t, rt, wantSequence)
}
