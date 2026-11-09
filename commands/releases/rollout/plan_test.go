package rollout_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/config/configtest"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// productionCompletedAndInProgress is the #740 case: a full rollout of 141
// and a staged rollout of 142 coexist on production.
const productionCompletedAndInProgress = `{"track":"production","releases":[` +
	`{"name":"141","status":"completed","versionCodes":["141"]},` +
	`{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2},` +
	`{"name":"140","status":"halted","versionCodes":["140"],"userFraction":0.1}]}`

func TestRunComplete_plan_namesTheSupersededReleaseWithoutWriting(t *testing.T) {
	rt := newStateFake(stateAPI{editID: "edit-plan", trackGetResp: productionCompletedAndInProgress})
	var payload output.Renderable
	stderr := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		var err error
		payload, err = rollout.RunComplete(rc, rollout.Input{Package: "com.example.app", Track: "production", VersionCode: 142, Plan: true})
		return err
	})

	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-plan/tracks/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-plan",
	}
	if got := apiCalls(rt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v, want read-only Edit %v", got, want)
	}
	if strings.Contains(stderr, "✓") || !strings.Contains(stderr, "nothing was sent") {
		t.Errorf("stderr = %q, want a plan note and no ✓ line", stderr)
	}

	var table bytes.Buffer
	if err := output.Render(&table, output.FormatTable, payload.Renderers()); err != nil {
		t.Fatalf("table render: %v", err)
	}
	wantTable := "track:      production\n" +
		"target:     142 [versionCodes 142], inProgress, userFraction 0.2\n" +
		"becomes:    completed\n" +
		"superseded: 141 [versionCodes 141], completed\n" +
		"kept:       140 [versionCodes 140], halted, userFraction 0.1\n"
	if table.String() != wantTable {
		t.Errorf("table =\n%s\nwant\n%s", table.String(), wantTable)
	}

	var js bytes.Buffer
	if err := output.Render(&js, output.FormatJSON, payload.Renderers()); err != nil {
		t.Fatalf("json render: %v", err)
	}
	var got struct {
		Plan       bool                              `json:"plan"`
		Status     string                            `json:"status"`
		Target     struct{ VersionCodes []string }   `json:"target"`
		Superseded []struct{ VersionCodes []string } `json:"superseded"`
		Kept       []struct{ VersionCodes []string } `json:"kept"`
	}
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("json %s: %v", js.String(), err)
	}
	if !got.Plan || got.Status != "completed" || got.Target.VersionCodes[0] != "142" ||
		len(got.Superseded) != 1 || got.Superseded[0].VersionCodes[0] != "141" ||
		len(got.Kept) != 1 || got.Kept[0].VersionCodes[0] != "140" {
		t.Errorf("json = %s, want plan:true, target 142, superseded 141, kept 140", js.String())
	}

	var md bytes.Buffer
	if err := output.Render(&md, output.FormatMarkdown, payload.Renderers()); err != nil {
		t.Fatalf("markdown render: %v", err)
	}
	if !strings.Contains(md.String(), "- **superseded**: 141 [versionCodes 141], completed\n") {
		t.Errorf("markdown = %q, want a superseded bullet naming 141", md.String())
	}
}

func TestRunComplete_plan_ambiguousTrackFailsLikeTheLivePath(t *testing.T) {
	rt := newStateFake(stateAPI{editID: "edit-plan", trackGetResp: productionCompletedAndInProgress})
	_, err := rollout.RunComplete(newRC(t, rt), rollout.Input{Package: "com.example.app", Track: "production", Plan: true})
	assertExit(t, err, 60)
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "PUT ") || strings.HasSuffix(c, ":commit") {
			t.Errorf("plan wrote to the track: %v", apiCalls(rt))
		}
	}
}

func TestRunHalt_plan_keepsTheLiveFractionAndSupersedesNothing(t *testing.T) {
	rt := newStateFake(stateAPI{editID: "edit-plan", trackGetResp: oneInProgressRelease})
	var payload output.Renderable
	confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		var err error
		payload, err = rollout.RunHalt(rc, rollout.Input{Package: "com.example.app", Track: "production", Plan: true})
		return err
	})
	var table bytes.Buffer
	if err := output.Render(&table, output.FormatTable, payload.Renderers()); err != nil {
		t.Fatalf("table render: %v", err)
	}
	for _, want := range []string{"becomes:    halted, userFraction 0.05\n", "superseded: none\n", "kept:       none\n"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("table = %q, want %q", table.String(), want)
		}
	}
}

func TestRun_planWithDryRun_returnsExit2BeforeAnyHTTP(t *testing.T) {
	rt := newStateFake(stateAPI{})
	_, err := rollout.RunComplete(newRC(t, rt), rollout.Input{Package: "com.example.app", Track: "production", Plan: true, DryRun: true})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected no HTTP, saw %v", apiCalls(rt))
	}
}

// Inside a pinned Edit a plan reads in place and stages nothing, so a commit
// flag passed with it must not be reported as staged in that Edit.
func TestRunComplete_plan_pinnedEdit_readsInPlaceWithoutStagedWarning(t *testing.T) {
	rt := newStateFake(stateAPI{trackGetResp: oneInProgressRelease})
	stderr := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		fsys := configtest.NewMemFS("/repo", "/home")
		if err := editpin.Write(fsys, "/repo/.gplay", "com.example.app", "edit-pinned"); err != nil {
			return err
		}
		rc.FS = fsys
		rc.Resolved = &config.Resolved{ProjectSharedPath: "/repo/.gplay/config.json"}
		in := rollout.Input{Package: "com.example.app", Track: "production", Plan: true}
		in.Commit.ChangesNotSentForReview = true
		_, err := rollout.RunComplete(rc, in)
		return err
	})
	want := "GET /androidpublisher/v3/applications/com.example.app/edits/edit-pinned/tracks/production"
	if got := apiCalls(rt); len(got) != 1 || got[0] != want {
		t.Errorf("calls = %v, want only %q", got, want)
	}
	if strings.Contains(stderr, "staged in open edit") {
		t.Errorf("stderr = %q, want no staged warning from a plan", stderr)
	}
}
