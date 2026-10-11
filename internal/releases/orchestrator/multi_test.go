package orchestrator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// writeAABNamed writes a fake artifact under name, so the artifacts of one
// call never share a path.
func writeAABNamed(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("fake-aab-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// releaseCodes decodes the versionCodes of the single release in the last
// tracks.update body.
func releaseCodes(t *testing.T, f *testkit.Fake) []string {
	t.Helper()
	var track struct {
		Releases []struct {
			VersionCodes []string `json:"versionCodes"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(trackUpdateReq(f), &track); err != nil || len(track.Releases) != 1 {
		t.Fatalf("tracks.update body %s: want one release (err=%v)", trackUpdateReq(f), err)
	}
	return track.Releases[0].VersionCodes
}

// TestUpload_multiArtifact_resultCarriesEveryCode asserts the orchestrator's
// N-in, N-out contract (#666): every uploaded code in argument order, the
// retained ones echoed, the headline versionCode the highest, and the wire
// release listing uploaded then retained codes.
func TestUpload_multiArtifact_resultCarriesEveryCode(t *testing.T) {
	rt, transport := newPlay(playAPI{editID: "edit-n", versionCodes: []int{143, 142}})

	result, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:            "com.example.app",
		Track:              "internal",
		Artifacts:          []orchestrator.Artifact{{Path: writeAABNamed(t, "a.aab")}, {Path: writeAABNamed(t, "b.aab")}},
		RetainVersionCodes: []int{41},
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := result.UploadedVersionCodes; len(got) != 2 || got[0] != 143 || got[1] != 142 {
		t.Errorf("UploadedVersionCodes = %v, want [143 142]", got)
	}
	if got := result.RetainedVersionCodes; len(got) != 1 || got[0] != 41 {
		t.Errorf("RetainedVersionCodes = %v, want [41]", got)
	}
	if result.VersionCode != 143 || result.ReleaseName != "143" {
		t.Errorf("(VersionCode, ReleaseName) = (%d, %q), want (143, 143)", result.VersionCode, result.ReleaseName)
	}
	if got := strings.Join(releaseCodes(t, rt), ","); got != "143,142,41" {
		t.Errorf("release versionCodes = %s, want 143,142,41", got)
	}
}

// TestUpload_multiArtifact_explicitEdit_uploadsAllCommitsNothing asserts a
// multi-artifact upload into an explicit Edit (`gplay edits begin`) uploads
// every artifact and updates the track in the pinned Edit, and never commits
// it: committing stays the user's call.
func TestUpload_multiArtifact_explicitEdit_uploadsAllCommitsNothing(t *testing.T) {
	rt, transport := newPlay(playAPI{
		versionCodes: []int{7, 8},
		insertHandler: func(testkit.Call) (int, string) {
			t.Fatalf("edits.insert called in explicit mode")
			return 0, ""
		},
		commitHandler: func(testkit.Call) (int, string) {
			t.Fatalf("edits.commit called in explicit mode: the user owns commit")
			return 0, ""
		},
	})

	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:        "com.example.app",
		Track:          "internal",
		Artifacts:      []orchestrator.Artifact{{Path: writeAABNamed(t, "a.aab")}, {Path: writeAABNamed(t, "b.aab")}},
		ExplicitEditID: "edit-pinned",
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	uploads := 0
	for _, c := range apiCalls(rt) {
		if c == "PUT /upload/androidpublisher/v3/applications/com.example.app/edits/edit-pinned/bundles" {
			uploads++
		}
	}
	if uploads != 2 {
		t.Errorf("saw %d bundle uploads into the pinned Edit, want 2; calls=%v", uploads, apiCalls(rt))
	}
	if got := strings.Join(releaseCodes(t, rt), ","); got != "7,8" {
		t.Errorf("release versionCodes = %s, want 7,8", got)
	}
}

// TestUpload_mappingWithSeveralArtifacts_exit2_noHTTP asserts the
// orchestrator refuses a mapping it cannot key to one versionCode, before
// any request, for callers other than the CLI.
func TestUpload_mappingWithSeveralArtifacts_exit2_noHTTP(t *testing.T) {
	rt, transport := newPlay(playAPI{})

	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package:     "com.example.app",
		Track:       "internal",
		Artifacts:   []orchestrator.Artifact{{Path: writeAABNamed(t, "a.aab")}, {Path: writeAABNamed(t, "b.aab")}},
		MappingPath: writeFakeMapping(t),
	})
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a refused Opts must make no request; calls=%v", apiCalls(rt))
	}
}

// TestUpload_noArtifact_exit2_noHTTP asserts an empty artifact list is an
// invalid Opts, never an Edit opened for nothing.
func TestUpload_noArtifact_exit2_noHTTP(t *testing.T) {
	rt, transport := newPlay(playAPI{})

	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app",
		Track:   "internal",
	})
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("a refused Opts must make no request; calls=%v", apiCalls(rt))
	}
}
