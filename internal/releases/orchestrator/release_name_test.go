package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

func uploadedReleaseNames(t *testing.T, body []byte) []string {
	t.Helper()
	var track struct {
		Releases []struct {
			Name string `json:"name"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(body, &track); err != nil {
		t.Fatalf("decode tracks.update body %s: %v", body, err)
	}
	names := make([]string, 0, len(track.Releases))
	for _, r := range track.Releases {
		names = append(names, r.Name)
	}
	return names
}

func TestUpload_releaseName_namesTheReleaseAndPromoteFindsIt(t *testing.T) {
	up, transport := newPlay(playAPI{editID: "edit-named", versionCode: 142})
	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app", Track: "beta", Artifacts: []orchestrator.Artifact{{Path: writeFakeAAB(t)}}, ReleaseName: "2.4.1",
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := uploadedReleaseNames(t, trackUpdateReq(up)); len(got) != 1 || got[0] != "2.4.1" {
		t.Fatalf("tracks.update release names = %v, want [2.4.1]", got)
	}

	var sent struct {
		Releases []json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(trackUpdateReq(up), &sent); err != nil {
		t.Fatalf("decode tracks.update body: %v", err)
	}
	source := `{"track":"beta","releases":[` +
		`{"name":"2.4.0","status":"halted","versionCodes":["141"]},` +
		string(sent.Releases[0]) + `]}`
	pr := newPromoteFake(promoteAPI{editID: "edit-promote", sourceTrackGetResp: source})

	result, err := orchestrator.Promote(context.Background(), &http.Client{Transport: pr}, orchestrator.PromoteOpts{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha", ReleaseName: "2.4.1",
	})
	if err != nil {
		t.Fatalf("Promote --release-name 2.4.1: %v", err)
	}
	if result.VersionCode != 142 {
		t.Errorf("promoted versionCode = %d, want 142 (the release named 2.4.1)", result.VersionCode)
	}
	if got := uploadedReleaseNames(t, trackUpdateReq(pr)); len(got) != 1 || got[0] != "2.4.1" {
		t.Errorf("promoted release names = %v, want [2.4.1] (name carried over)", got)
	}
}

func TestUpload_noReleaseName_keepsVersionCodeName(t *testing.T) {
	up, transport := newPlay(playAPI{editID: "edit-default", versionCode: 142})
	if _, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app", Track: "beta", Artifacts: []orchestrator.Artifact{{Path: writeFakeAAB(t)}},
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := uploadedReleaseNames(t, trackUpdateReq(up)); len(got) != 1 || got[0] != "142" {
		t.Errorf("tracks.update release names = %v, want [142]", got)
	}
}

func TestUpload_dryRun_releaseName_previewed(t *testing.T) {
	rt, transport := newPlay(playAPI{})
	result, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app", Track: "beta", Artifacts: []orchestrator.Artifact{{Path: writeFakeAAB(t)}}, ReleaseName: "2.4.1", DryRun: true,
	})
	if err != nil {
		t.Fatalf("Upload(dry-run): %v", err)
	}
	if result.ReleaseName != "2.4.1" {
		t.Errorf("result.ReleaseName = %q, want 2.4.1", result.ReleaseName)
	}
	if touched(rt) {
		t.Errorf("dry-run made HTTP calls: %v", apiCalls(rt))
	}
}

func TestUpload_blankReleaseName_invalidOpts_noHTTP(t *testing.T) {
	rt, transport := newPlay(playAPI{editID: "edit-blank"})
	_, err := orchestrator.Upload(context.Background(), &http.Client{Transport: transport}, orchestrator.Opts{
		Package: "com.example.app", Track: "beta", Artifacts: []orchestrator.Artifact{{Path: writeFakeAAB(t)}}, ReleaseName: "   ",
	})
	var invalid *orchestrator.InvalidOptsError
	if !errors.As(err, &invalid) {
		t.Errorf("err = %v (%T), want *orchestrator.InvalidOptsError", err, err)
	}
	if touched(rt) {
		t.Errorf("blank name reached the transport: %v", apiCalls(rt))
	}
}
