package promote_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const (
	betaSource                = `{"track":"beta","releases":[{"name":"2.5.0","status":"completed","versionCodes":["150"]}]}`
	productionHaltedAndDraft  = `{"track":"production","releases":[{"name":"2.4.0","status":"halted","versionCodes":["140"],"userFraction":0.1},{"name":"2.4.1","status":"draft","versionCodes":["141"]}]}`
	wantProductionPlanAsTable = "from:       beta\n" +
		"source:     2.5.0 [versionCodes 150], completed\n" +
		"to:         production\n" +
		"becomes:    draft\n" +
		"replaced:   2.4.0 [versionCodes 140], halted; 2.4.1 [versionCodes 141], draft\n"
)

func TestRun_sameTrack_exit2BeforeAnyHTTP(t *testing.T) {
	rt := newPromoteFake(promoteAPI{editID: "edit-same", sourceTrackGetResp: betaSource})
	rc, _ := newRC(t, rt)
	_, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "production", ToTrack: "production"})
	if code := exit.For(err); code != 2 {
		t.Errorf("exit = %d (err %v), want 2", code, err)
	}
	if touched(rt) {
		t.Errorf("calls = %v, want none", apiCalls(rt))
	}
}

func TestRun_planWithDryRun_exit2BeforeAnyHTTP(t *testing.T) {
	rt := newPromoteFake(promoteAPI{editID: "edit-both"})
	rc, _ := newRC(t, rt)
	_, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "production", Plan: true, DryRun: true})
	if code := exit.For(err); code != 2 {
		t.Errorf("exit = %d (err %v), want 2", code, err)
	}
	if touched(rt) {
		t.Errorf("calls = %v, want none", apiCalls(rt))
	}
}

func TestRun_plan_namesTheReplacedHaltedAndDraftReleases(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-plan",
		sourceTrackGetResp: betaSource,
		destTrack:          "production",
		destTrackGetResp:   productionHaltedAndDraft,
	})
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	payload, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "production", Plan: true})
	if err != nil {
		t.Fatalf("Run --plan: %v", err)
	}
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "PUT ") || strings.HasSuffix(c, ":commit") {
			t.Errorf("plan wrote: %s", c)
		}
	}
	if strings.Contains(stderr.String(), "✓") || !strings.Contains(stderr.String(), "nothing was sent") {
		t.Errorf("stderr = %q, want a plan note and no ✓ line", stderr.String())
	}

	var table bytes.Buffer
	if err := output.Render(&table, output.FormatTable, payload.Renderers()); err != nil {
		t.Fatalf("table render: %v", err)
	}
	if table.String() != wantProductionPlanAsTable {
		t.Errorf("table =\n%s\nwant\n%s", table.String(), wantProductionPlanAsTable)
	}

	var js bytes.Buffer
	if err := output.Render(&js, output.FormatJSON, payload.Renderers()); err != nil {
		t.Fatalf("json render: %v", err)
	}
	var got struct {
		Plan     bool `json:"plan"`
		Replaced []struct {
			Status string `json:"status"`
		} `json:"replaced"`
	}
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("plan JSON %s: %v", js.String(), err)
	}
	if !got.Plan || len(got.Replaced) != 2 || got.Replaced[0].Status != "halted" || got.Replaced[1].Status != "draft" {
		t.Errorf("plan JSON = %s, want plan:true and the halted and draft releases under replaced", js.String())
	}
}

func TestRun_replacedReleasesAppearOnTheConfirmationLine(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-live",
		sourceTrackGetResp: betaSource,
		destTrack:          "production",
		destTrackGetResp:   productionHaltedAndDraft,
	})
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if _, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "production"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "replaced versionCode 140, 141") {
		t.Errorf("stderr = %q, want the replaced versionCodes on the ✓ line", stderr.String())
	}
}

func TestRun_destinationMissing_hintsTracksCreate(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-miss",
		sourceTrackGetResp: betaSource,
		destTrack:          "qa-team",
		destTrackGetStatus: http.StatusNotFound,
	})
	rc, _ := newRC(t, rt)
	_, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "qa-team", Plan: true})
	if err == nil || !strings.Contains(err.Error(), "gplay tracks create qa-team") {
		t.Errorf("err = %v, want the `gplay tracks create qa-team` hint", err)
	}
	if code := exit.For(err); code != 30 {
		t.Errorf("exit = %d, want 30", code)
	}
}

func TestRun_dryRun_jsonCarriesDryRunRequiresAndTheRequestedVersionCode(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	rc, _ := newRC(t, rt)
	payload, err := promote.Run(rc, promote.Input{
		Package: "com.example.app", FromTrack: "alpha", ToTrack: "production", VersionCode: 1042, Complete: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Run --dry-run: %v", err)
	}
	if touched(rt) {
		t.Errorf("dry-run calls = %v, want none", apiCalls(rt))
	}
	var js bytes.Buffer
	if err := output.Render(&js, output.FormatJSON, payload.Renderers()); err != nil {
		t.Fatalf("json render: %v", err)
	}
	var got struct {
		DryRun              bool     `json:"dryRun"`
		VersionCode         int      `json:"versionCode"`
		Track               string   `json:"track"`
		Status              string   `json:"status"`
		ReleaseName         string   `json:"releaseName"`
		Requires            []string `json:"requires"`
		ReplacesDestination bool     `json:"replacesDestination"`
	}
	if err := json.Unmarshal(js.Bytes(), &got); err != nil {
		t.Fatalf("dry-run JSON %s: %v", js.String(), err)
	}
	if !got.DryRun || got.VersionCode != 1042 || !slices.Equal(got.Requires, []string{"confirm"}) || !got.ReplacesDestination {
		t.Errorf("dry-run JSON = %s, want dryRun, versionCode 1042, requires [confirm], replacesDestination", js.String())
	}
	if got.Track != "production" || got.Status != "completed" || got.ReleaseName != "(dry-run)" {
		t.Errorf("dry-run JSON = %s, want the existing track/status/releaseName fields unchanged", js.String())
	}

	var table bytes.Buffer
	if err := output.Render(&table, output.FormatTable, payload.Renderers()); err != nil {
		t.Fatalf("table render: %v", err)
	}
	if !strings.Contains(table.String(), "replaces:     every release on production") {
		t.Errorf("table = %q, want the replaces line", table.String())
	}
}
