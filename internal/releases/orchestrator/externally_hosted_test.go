package orchestrator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// hostedAPK is a well-formed ExternallyHostedApk resource for com.example.app.
const hostedAPK = `{"packageName":"com.example.app","externallyHostedUrl":"https://cdn.example.com/app-310.apk","versionCode":310,"fileSize":"1048576"}`

// writeHosted writes body as an externally hosted APK resource file.
func writeHosted(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ext.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_externallyHosted_callSequenceAndRelease asserts the #546 flow:
// edits.insert → apks.addexternallyhosted → tracks.update → edits.commit,
// the resource sent verbatim, and the release carrying the versionCode Google
// returned.
func TestUpload_externallyHosted_callSequenceAndRelease(t *testing.T) {
	rt, transport := newPlay(playAPI{editID: "edit-h", hostedVersionCode: 310})

	result, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: writeHosted(t, hostedAPK), Format: orchestrator.FormatExternallyHosted}},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-h/apks/externallyHosted",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-h/tracks/internal",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-h:commit",
	}
	if got := apiCalls(rt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var sent struct {
		ExternallyHostedApk json.RawMessage `json:"externallyHostedApk"`
	}
	if err := json.Unmarshal(lastBody(rt, http.MethodPost, "/apks/externallyHosted"), &sent); err != nil || string(sent.ExternallyHostedApk) != hostedAPK {
		t.Errorf("externallyHostedApk sent = %s (err=%v), want the file verbatim", sent.ExternallyHostedApk, err)
	}
	if got := strings.Join(releaseCodes(t, rt), ","); got != "310" {
		t.Errorf("release versionCodes = %s, want 310", got)
	}
	if result.VersionCode != 310 || result.ReleaseName != "310" {
		t.Errorf("(VersionCode, ReleaseName) = (%d, %q), want (310, 310)", result.VersionCode, result.ReleaseName)
	}
}

// TestUpload_externallyHostedWithBundle_releaseHoldsBoth asserts an
// externally hosted APK combines with an uploaded AAB (#666): one Edit, one
// tracks.update, and the release listing both versionCodes, then the
// retained one.
func TestUpload_externallyHostedWithBundle_releaseHoldsBoth(t *testing.T) {
	rt, transport := newPlay(playAPI{editID: "edit-m", versionCode: 311, hostedVersionCode: 310})

	result, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
		Artifacts: []orchestrator.Artifact{
			{Path: writeAABNamed(t, "phone.aab"), Format: orchestrator.FormatBundle},
			{Path: writeHosted(t, hostedAPK), Format: orchestrator.FormatExternallyHosted},
		},
		RetainVersionCodes: []int{41},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := strings.Join(releaseCodes(t, rt), ","); got != "311,310,41" {
		t.Errorf("release versionCodes = %s, want 311,310,41", got)
	}
	if got := result.UploadedVersionCodes; len(got) != 2 || got[0] != 311 || got[1] != 310 {
		t.Errorf("UploadedVersionCodes = %v, want [311 310]", got)
	}
	commits := 0
	for _, c := range apiCalls(rt) {
		if strings.HasSuffix(c, ":commit") {
			commits++
		}
	}
	if commits != 1 {
		t.Errorf("commits = %d, want 1; calls=%v", commits, apiCalls(rt))
	}
}

// TestUpload_externallyHosted_badResource_exit20_beforeInsert asserts the two
// local checks gplay owns fail before edits.insert (no request at all, token
// exchange included), live and on --dry-run alike.
func TestUpload_externallyHosted_badResource_exit20_beforeInsert(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":   `{"packageName":"com.example.app",`,
		"not an object":    `["com.example.app"]`,
		"null":             `null`,
		"package mismatch": `{"packageName":"com.other.app","externallyHostedUrl":"https://cdn.example.com/a.apk"}`,
		"package not text": `{"packageName":42}`,
	}
	for name, body := range cases {
		for _, dryRun := range []bool{false, true} {
			t.Run(name+"/dryRun="+strconv.FormatBool(dryRun), func(t *testing.T) {
				rt, transport := newPlay(playAPI{
					insertHandler: func(testkit.Call) (int, string) {
						t.Fatalf("edits.insert reached with a bad resource")
						return 0, ""
					},
				})
				_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
					Package:   "com.example.app",
					Track:     "internal",
					Artifacts: []orchestrator.Artifact{{Path: writeHosted(t, body), Format: orchestrator.FormatExternallyHosted}},
					DryRun:    dryRun,
				})
				if got := exitCode(err); got != 20 {
					t.Fatalf("dryRun=%v: exit = %d, want 20; err=%v", dryRun, got, err)
				}
				if touched(rt) {
					t.Errorf("dryRun=%v: a bad resource must make no request; calls=%v", dryRun, apiCalls(rt))
				}
			})
		}
	}
}

// TestUpload_externallyHosted_noPackageName_isGooglesCall asserts a resource
// without packageName is not refused locally: only a present, different
// package is gplay's to catch.
func TestUpload_externallyHosted_noPackageName_isGooglesCall(t *testing.T) {
	rt, transport := newPlay(playAPI{editID: "edit-n", hostedVersionCode: 7})

	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:   "com.example.app",
		Track:     "internal",
		Artifacts: []orchestrator.Artifact{{Path: writeHosted(t, `{"externallyHostedUrl":"https://cdn.example.com/a.apk"}`), Format: orchestrator.FormatExternallyHosted}},
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := strings.Join(releaseCodes(t, rt), ","); got != "7" {
		t.Errorf("release versionCodes = %s, want 7", got)
	}
}
