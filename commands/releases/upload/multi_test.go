package upload_test

// Multi-artifact releases and retained versionCodes (#666): one Edit, every
// artifact uploaded, one tracks.update listing every code, one commit, or
// nothing at all. Every scenario runs through the suite's testkit fake, so
// the recorded requests are the proof.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/upload"
	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// writeNamedAAB writes a valid AAB for com.example.app under name, so two
// artifacts of one call never share a path.
func writeNamedAAB(t *testing.T, name string) string {
	t.Helper()
	return artifacttest.AAB(t, t.TempDir(), name, "com.example.app")
}

// trackRelease decodes the single release of the last tracks.update body.
func trackRelease(t *testing.T, f *testkit.Fake) (name string, codes []string) {
	t.Helper()
	body := trackUpdateReq(f)
	if body == nil {
		t.Fatalf("no tracks.update was sent; calls=%v", apiCalls(f))
	}
	var track struct {
		Releases []struct {
			Name         string   `json:"name"`
			VersionCodes []string `json:"versionCodes"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &track); err != nil {
		t.Fatalf("decode tracks.update body %s: %v", body, err)
	}
	if len(track.Releases) != 1 {
		t.Fatalf("tracks.update carries %d releases, want 1: %s", len(track.Releases), body)
	}
	return track.Releases[0].Name, track.Releases[0].VersionCodes
}

// TestRun_twoAABs_oneEditTwoUploadsOneTrackUpdateOneCommit is the core
// acceptance criterion: two bundles ride one Edit, each through its own
// bundles.upload, and a single tracks.update lists both codes before a single
// commit. The release is named after the highest uploaded code.
func TestRun_twoAABs_oneEditTwoUploadsOneTrackUpdateOneCommit(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-multi", versionCodes: []int{142, 143}})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeNamedAAB(t, "phone.aab"), writeNamedAAB(t, "wear.aab")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertSequence(t, rt, []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-multi/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-multi/bundles",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-multi/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-multi/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-multi/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-multi:commit",
	})
	name, codes := trackRelease(t, rt)
	if strings.Join(codes, ",") != "142,143" {
		t.Errorf("release versionCodes = %v, want [142 143]", codes)
	}
	if name != "143" {
		t.Errorf("release name = %q, want 143 (the highest uploaded versionCode)", name)
	}
	if !strings.Contains(stderr.String(), "uploaded versionCodes 142, 143 to track") {
		t.Errorf("✓ line does not list both codes; stderr=%q", stderr.String())
	}
}

// TestRun_releaseName_defaultsToHighestCode_notTheLast asserts the default
// name follows the highest uploaded versionCode even when it is not the last
// artifact passed.
func TestRun_releaseName_defaultsToHighestCode_notTheLast(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-order", versionCodes: []int{150, 149}})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeNamedAAB(t, "a.aab"), writeNamedAAB(t, "b.aab")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	name, codes := trackRelease(t, rt)
	if name != "150" || strings.Join(codes, ",") != "150,149" {
		t.Errorf("release = (%q, %v), want (150, [150 149]): argument order kept, named after the highest", name, codes)
	}
}

// TestRun_retainVersionCodes_appendedWithoutExtraRead asserts
// --retain-version-code appends the codes to the release verbatim, after the
// uploaded one, and that no read call (tracks.get or similar) validates them:
// an unknown code is Google's to refuse.
func TestRun_retainVersionCodes_appendedWithoutExtraRead(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-keep", versionCode: 142})
	rc, _ := newRC(t, transport)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := upload.Run(rc, upload.Input{
		Package:            "com.example.app",
		Track:              "internal",
		Artifacts:          []string{writeFakeAAB(t)},
		RetainVersionCodes: []int{41, 42},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertSequence(t, rt, []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-keep/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-keep/bundles",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-keep/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-keep:commit",
	})
	name, codes := trackRelease(t, rt)
	if strings.Join(codes, ",") != "142,41,42" {
		t.Errorf("release versionCodes = %v, want [142 41 42]", codes)
	}
	if name != "142" {
		t.Errorf("release name = %q, want 142: retained codes never name the release", name)
	}
	if !strings.Contains(stderr.String(), "uploaded versionCode 142 to track") || !strings.Contains(stderr.String(), "retained 41, 42") {
		t.Errorf("✓ line should name the upload and the retained codes; stderr=%q", stderr.String())
	}
}

// TestRun_secondUploadFails_discardsEdit_noTrackUpdateNoCommit is the
// all-or-nothing contract: when the second artifact's upload fails, the
// implicit Edit is discarded, no tracks.update or commit is ever sent, and
// the exit code is the one a single failed upload returns.
func TestRun_secondUploadFails_discardsEdit_noTrackUpdateNoCommit(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-half", versionCodes: []int{142, 143}, failArtifact: 2})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeNamedAAB(t, "a.aab"), writeNamedAAB(t, "b.aab")},
	})
	if err == nil {
		t.Fatal("Run succeeded although the second upload failed")
	}
	assertSequence(t, rt, []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-half/bundles",
		"PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-half/bundles",
		"POST /upload/androidpublisher/v3/applications/com.example.app/edits/edit-half/bundles",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-half",
	})

	// The same refusal on a single-artifact upload: the exit code must match.
	_, single := newUploadTransport(uploadAPI{editID: "edit-one", failArtifact: 1})
	rc1, _ := newRC(t, single)
	_, singleErr := upload.Run(rc1, upload.Input{Package: "com.example.app", Track: "internal", Artifacts: []string{writeFakeAAB(t)}})
	if singleErr == nil {
		t.Fatal("single-artifact baseline did not fail")
	}
	if got, want := exit.For(err), exit.For(singleErr); got != want {
		t.Errorf("exit = %d, want %d (a single failed upload's exit code); err=%v", got, want, err)
	}
}

// TestRun_preflightMismatchOnSecondArtifact_failsBeforeEditsInsert asserts
// the preflight checks every artifact before the Edit is opened: a mismatch
// on the second one fails the whole call with nothing on the wire, not even
// the token exchange.
func TestRun_preflightMismatchOnSecondArtifact_failsBeforeEditsInsert(t *testing.T) {
	other := artifacttest.AAB(t, t.TempDir(), "other.aab", "com.other.app")
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-never"})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeFakeAAB(t), other},
	})
	if got := exit.For(err); got != 20 {
		t.Fatalf("exit = %d, want 20 (preflight refusal); err=%v", got, err)
	}
	if !strings.Contains(err.Error(), `"com.other.app"`) {
		t.Errorf("refusal %q does not name the second artifact's package", err)
	}
	if touched(rt) {
		t.Errorf("a refused artifact set must make no network call; calls=%v", apiCalls(rt))
	}
}

// TestRun_mappingWithTwoArtifacts_exit2_noNetwork asserts --mapping is a
// usage error with several artifacts: a mapping belongs to one versionCode,
// and guessing which would symbolicate the wrong build's crashes.
func TestRun_mappingWithTwoArtifacts_exit2_noNetwork(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeNamedAAB(t, "a.aab"), writeNamedAAB(t, "b.aab")},
		Mapping:   writeFakeMapping(t),
	})
	if got := exit.For(err); got != 2 {
		t.Fatalf("exit = %d, want 2; err=%v", got, err)
	}
	for _, want := range []string{"--mapping", "single artifact", "releases mappings upload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if touched(rt) {
		t.Errorf("a usage error must make no network call; calls=%v", apiCalls(rt))
	}
}

// TestRun_formatDisagreesWithAnExtension_multi_exit2 asserts one --format
// covering several artifacts is refused when an artifact's extension names
// the other container, before any network call.
func TestRun_formatDisagreesWithAnExtension_multi_exit2(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	_, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{writeFakeAPK(t), writeNamedAAB(t, "b.aab")},
		Format:    "apk",
	})
	if got := exit.For(err); got != 2 {
		t.Fatalf("exit = %d, want 2; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "--format apk disagrees") || !strings.Contains(err.Error(), "b.aab") {
		t.Errorf("error %q does not name the flag and the disagreeing artifact", err)
	}
	if touched(rt) {
		t.Errorf("a usage error must make no network call; calls=%v", apiCalls(rt))
	}
}

// TestRun_formatOverride_multi_appliesToEveryArtifact asserts --format covers
// artifacts whose extension says nothing (.bin): both ride the APK endpoint.
func TestRun_formatOverride_multi_appliesToEveryArtifact(t *testing.T) {
	a := artifacttest.APK(t, t.TempDir(), "arm64.bin", "com.example.app")
	b := artifacttest.APK(t, t.TempDir(), "x86.apk", "com.example.app")
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-abi", versionCodes: []int{301, 302}})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []string{a, b},
		Format:    "apk",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	apks := 0
	for _, c := range apiCalls(rt) {
		if strings.HasSuffix(c, "/bundles") {
			t.Errorf("--format apk still hit the bundles endpoint: %q", c)
		}
		if strings.HasPrefix(c, "PUT ") && strings.HasSuffix(c, "/apks") {
			apks++
		}
	}
	if apks != 2 {
		t.Errorf("saw %d apks.upload, want 2; calls=%v", apks, apiCalls(rt))
	}
}

// TestRun_mixedAABAndAPK_deviceTierConfigOnTheBundleOnly asserts AAB/APK
// detection stays per file (a mixed set is Google's to accept or refuse)
// and --device-tier-config reaches the bundle upload, never the APK one.
func TestRun_mixedAABAndAPK_deviceTierConfigOnTheBundleOnly(t *testing.T) {
	rt, transport := newUploadTransport(uploadAPI{editID: "edit-mix", versionCodes: []int{10, 11}})
	rc, _ := newRC(t, transport)

	if _, err := upload.Run(rc, upload.Input{
		Package:          "com.example.app",
		Track:            "internal",
		Artifacts:        []string{writeFakeAAB(t), writeFakeAPK(t)},
		DeviceTierConfig: "LATEST",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range rt.Calls() {
		if c.Method != http.MethodPost {
			continue
		}
		q, err := url.ParseQuery(c.Query)
		if err != nil {
			t.Fatalf("parse query %q: %v", c.Query, err)
		}
		switch {
		case strings.HasSuffix(c.Path, "/bundles") && q.Get("deviceTierConfigId") != "LATEST":
			t.Errorf("bundles.upload query %q lacks deviceTierConfigId=LATEST", c.Query)
		case strings.HasSuffix(c.Path, "/apks") && q.Has("deviceTierConfigId"):
			t.Errorf("apks.upload query %q carries a device tier config", c.Query)
		}
	}
	if _, codes := trackRelease(t, rt); strings.Join(codes, ",") != "10,11" {
		t.Errorf("release versionCodes = %v, want [10 11]", codes)
	}
}

// TestRun_dryRun_multi_reportsPlannedUploadsWithoutNetwork asserts the
// --dry-run preview of several artifacts lists each planned upload and the
// retained codes, with nothing on the wire, and freezes its JSON shape.
func TestRun_dryRun_multi_reportsPlannedUploadsWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	a := artifacttest.AAB(t, dir, "phone.aab", "com.example.app")
	b := artifacttest.AAB(t, dir, "wear.aab", "com.example.app")
	rt, transport := newUploadTransport(uploadAPI{})
	rc, _ := newRC(t, transport)

	r, err := upload.Run(rc, upload.Input{
		Package:            "com.example.app",
		Track:              "internal",
		Artifacts:          []string{a, b},
		RetainVersionCodes: []int{41},
		DryRun:             true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run made network calls: %v", apiCalls(rt))
	}
	// The temp dir differs per run: pin the paths to stable names for the golden.
	got := strings.ReplaceAll(string(outputtest.RenderJSON(t, r)), dir+"/", "")
	testkit.Golden(t, "dry_run_multi.json.golden", []byte(got))
}

// TestRender_multi_listsEveryCodeInEveryFormat asserts the human formats
// and the gplay-authored JSON fallback show every uploaded and retained
// code for a multi-artifact release.
func TestRender_multi_listsEveryCodeInEveryFormat(t *testing.T) {
	p := upload.Payload{Result: &orchestrator.Result{
		VersionCode:          143,
		Track:                "beta",
		ReleaseName:          "143",
		Status:               "completed",
		UserFraction:         1,
		UploadedVersionCodes: []int{142, 143},
		RetainedVersionCodes: []int{41},
	}}
	outputtest.GoldenJSON(t, "fallback_multi.json.golden", p)

	var table, md bytes.Buffer
	if err := output.Render(&table, output.FormatTable, p.Renderers()); err != nil {
		t.Fatalf("render table: %v", err)
	}
	if err := output.Render(&md, output.FormatMarkdown, p.Renderers()); err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	wantTable := "versionCodes: 142, 143\nretained:     41\ntrack:        beta\nstatus:       completed\nuserFraction: 1\nreleaseName:  143\n"
	if table.String() != wantTable {
		t.Errorf("table =\n%s\nwant\n%s", table.String(), wantTable)
	}
	wantMD := "- **versionCodes**: 142, 143\n- **retained**: 41\n- **track**: beta\n- **status**: completed\n- **userFraction**: 1\n- **releaseName**: 143\n"
	if md.String() != wantMD {
		t.Errorf("markdown =\n%s\nwant\n%s", md.String(), wantMD)
	}
}

// TestNewCommand_documentsBothShapes asserts --help teaches both shapes: the
// variadic usage line, an example per shape, and the retained-code flag.
func TestNewCommand_documentsBothShapes(t *testing.T) {
	cmd := upload.NewCommand(kernel.Boot{})
	if cmd.Use != "upload <artifact>..." {
		t.Errorf("Use = %q, want the variadic upload <artifact>...", cmd.Use)
	}
	for _, want := range []string{
		"gplay releases upload app-release.aab --track internal",
		"gplay releases upload phone.aab wear.aab --track beta --retain-version-code 41",
	} {
		if !strings.Contains(cmd.Example, want) {
			t.Errorf("Example lacks %q", want)
		}
	}
	for _, want := range []string{"several artifacts", "all or nothing", "--retain-version-code"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("Long does not explain %q", want)
		}
	}
	if cmd.Flags().Lookup("retain-version-code") == nil {
		t.Error("releases upload has no --retain-version-code flag")
	}
}

// TestRender_single_tableAndMarkdownUnchanged pins the single-artifact human
// shapes byte for byte: the multi-artifact lines must not leak into them.
func TestRender_single_tableAndMarkdownUnchanged(t *testing.T) {
	p := upload.Payload{Result: &orchestrator.Result{
		VersionCode:          142,
		Track:                "internal",
		ReleaseName:          "142",
		Status:               "completed",
		UserFraction:         1,
		UploadedVersionCodes: []int{142},
	}}
	var table, md bytes.Buffer
	if err := output.Render(&table, output.FormatTable, p.Renderers()); err != nil {
		t.Fatalf("render table: %v", err)
	}
	if err := output.Render(&md, output.FormatMarkdown, p.Renderers()); err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if want := "versionCode:  142\ntrack:        internal\nstatus:       completed\nuserFraction: 1\nreleaseName:  142\n"; table.String() != want {
		t.Errorf("table =\n%q\nwant\n%q", table.String(), want)
	}
	if want := "- **versionCode**: 142\n- **track**: internal\n- **status**: completed\n- **userFraction**: 1\n- **releaseName**: 142\n"; md.String() != want {
		t.Errorf("markdown =\n%q\nwant\n%q", md.String(), want)
	}
	// The JSON fallback of one artifact keeps the historical shape too.
	if got := string(outputtest.RenderJSON(t, p)); strings.Contains(got, "uploadedVersionCodes") {
		t.Errorf("single-artifact JSON grew the multi-artifact field:\n%s", got)
	}
}
