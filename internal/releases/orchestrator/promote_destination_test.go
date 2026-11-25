package orchestrator_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const betaSource = `{"track":"beta","releases":[{"name":"2.5.0","status":"completed","versionCodes":["150"]}]}`

const productionHaltedAndDraft = `{"track":"production","releases":[` +
	`{"name":"2.4.0","status":"halted","versionCodes":["140"],"userFraction":0.1},` +
	`{"name":"2.4.1","status":"draft","versionCodes":["141"]}]}`

func TestPromote_sameTrack_exit2BeforeAnyHTTP(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		rt := newPromoteFake(promoteAPI{editID: "edit-same", sourceTrackGetResp: betaSource})
		_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
			Package: "com.example.app", FromTrack: "production", ToTrack: "production", DryRun: dryRun,
		})
		var invalid *orchestrator.InvalidOptsError
		if !errors.As(err, &invalid) || exit.For(err) != 2 {
			t.Errorf("dryRun=%v: err = %v (exit %d), want *InvalidOptsError exit 2", dryRun, err, exit.For(err))
		}
		if touched(rt) {
			t.Errorf("dryRun=%v: calls = %v, want none", dryRun, apiCalls(rt))
		}
	}
}

func TestPromote_countryTargetingFollowsTheFraction(t *testing.T) {
	targeting := `{"countries":["FR","DE"],"includeRestOfWorld":false}`
	staged, _ := orchestrator.RequestedState(false, false, true, 0.1)
	complete, _ := orchestrator.RequestedState(false, true, false, 0)
	cases := []struct {
		name  string
		to    string
		state orchestrator.ReleaseState
		kept  bool
	}{
		{"a staged copy to production keeps it verbatim", "production", staged, true},
		{"a completed copy cannot carry it", "production", complete, false},
		{"a staged copy to another track cannot carry it", "beta", staged, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newPromoteFake(promoteAPI{
				editID:             "edit-ct",
				sourceTrackGetResp: `{"track":"beta","releases":[{"name":"2.5.0","status":"completed","versionCodes":["150"],"countryTargeting":` + targeting + `}]}`,
				destTrack:          tc.to,
				destTrackGetResp:   `{"track":"` + tc.to + `","releases":[]}`,
			})
			if _, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "alpha", ToTrack: tc.to, State: tc.state, Confirm: true,
			}); err != nil {
				t.Fatalf("Promote: %v", err)
			}
			body := string(trackUpdateReq(rt))
			if got := strings.Contains(body, `"countryTargeting":`+targeting); got != tc.kept {
				t.Errorf("countryTargeting carried = %v, want %v; tracks.update body = %s", got, tc.kept, body)
			}
		})
	}
}

func TestPromote_reportsTheDestinationReleasesItReplaced(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-replace",
		sourceTrackGetResp: betaSource,
		destTrack:          "production",
		destTrackGetResp:   productionHaltedAndDraft,
	})
	result, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "production",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if want := []int{140, 141}; !slices.Equal(result.ReplacedVersionCodes, want) {
		t.Errorf("ReplacedVersionCodes = %v, want %v", result.ReplacedVersionCodes, want)
	}
	if body := string(trackUpdateReq(rt)); strings.Contains(body, `"140"`) || strings.Contains(body, `"141"`) {
		t.Errorf("tracks.update body = %s, want the promoted release alone", body)
	}
}

func TestPromote_plan_listsTheReleasesItWouldReplaceWithoutWriting(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-plan",
		sourceTrackGetResp: betaSource,
		destTrack:          "production",
		destTrackGetResp:   productionHaltedAndDraft,
	})
	complete, _ := orchestrator.RequestedState(false, true, false, 0)
	result, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "production", State: complete, Plan: true,
	})
	if err != nil {
		t.Fatalf("Promote --plan without --confirm: %v", err)
	}
	want := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-plan/tracks/beta",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-plan/tracks/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-plan",
	}
	if got := apiCalls(rt); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want read-only Edit %v", got, want)
	}
	plan := result.Promotion
	if plan == nil {
		t.Fatal("Result.Promotion is nil on a plan")
	}
	if plan.Source.Name != "2.5.0" || plan.Status != "completed" || result.VersionCode != 150 {
		t.Errorf("plan = %+v (versionCode %d), want 2.5.0 → completed, versionCode 150", plan, result.VersionCode)
	}
	var replaced []string
	for _, r := range plan.Replaced {
		replaced = append(replaced, r.Name+":"+r.Status)
	}
	if want := []string{"2.4.0:halted", "2.4.1:draft"}; !slices.Equal(replaced, want) {
		t.Errorf("Replaced = %v, want %v", replaced, want)
	}
}

func TestPromote_destinationReadFailure_isMarked(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-dest",
		sourceTrackGetResp: betaSource,
		getHandler: func(c testkit.Call) (int, string) {
			if strings.HasSuffix(c.Path, "/tracks/qa-team") {
				return http.StatusNotFound, `{"error":{"code":404,"message":"Track not found."}}`
			}
			return http.StatusOK, betaSource
		},
	})
	_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "qa-team",
	})
	var dest *orchestrator.DestinationReadError
	if !errors.As(err, &dest) || exit.For(err) != 30 {
		t.Errorf("err = %v (exit %d), want *DestinationReadError keeping exit 30", err, exit.For(err))
	}
}

func TestPromote_dryRun_carriesRequestedVersionCodeAndRequires(t *testing.T) {
	complete, _ := orchestrator.RequestedState(false, true, false, 0)
	cases := []struct {
		name string
		to   string
		want []string
	}{
		{"production complete needs confirm", "production", []string{"confirm"}},
		{"beta needs nothing", "beta", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := orchestrator.Promote(context.Background(), nil, orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "alpha", ToTrack: tc.to, VersionCode: 1042, State: complete, DryRun: true,
			})
			if err != nil {
				t.Fatalf("Promote --dry-run: %v", err)
			}
			if result.VersionCode != 1042 {
				t.Errorf("VersionCode = %d, want the requested 1042", result.VersionCode)
			}
			if !slices.Equal(result.Requires, tc.want) {
				t.Errorf("Requires = %v, want %v", result.Requires, tc.want)
			}
		})
	}
}

func TestPromote_plan_refusesAnOverrideThatDropsASourceLocale_likeTheLiveRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Fresh English notes"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	source := `{"track":"beta","releases":[{"name":"2.5.0","status":"completed","versionCodes":["150"],` +
		`"releaseNotes":[{"language":"en-US","text":"English"},{"language":"fr-FR","text":"Français"}]}]}`
	run := func(plan bool) error {
		rt := newPromoteFake(promoteAPI{
			editID:             "edit-notes",
			sourceTrackGetResp: source,
			destTrack:          "production",
			destTrackGetResp:   productionHaltedAndDraft,
			updateHandler: func(c testkit.Call) (int, string) {
				t.Errorf("plan=%v: tracks.update sent despite a dropped locale: %s", plan, c.Path)
				return http.StatusInternalServerError, ""
			},
		})
		_, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
			Package: "com.example.app", FromTrack: "beta", ToTrack: "production", ReleaseNotesDir: dir, Plan: plan,
		})
		return err
	}
	planErr, liveErr := run(true), run(false)
	if planErr == nil || exit.For(planErr) != 2 || !strings.Contains(planErr.Error(), "fr-FR") {
		t.Fatalf("plan err = %v (exit %d), want exit 2 naming fr-FR", planErr, exit.For(planErr))
	}
	if liveErr == nil || planErr.Error() != liveErr.Error() {
		t.Errorf("plan err = %q, live err = %v, want the same refusal", planErr, liveErr)
	}
}

func TestPromote_plan_withACompleteOverrideIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Fresh English notes"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	source := `{"track":"beta","releases":[{"name":"2.5.0","status":"completed","versionCodes":["150"],` +
		`"releaseNotes":[{"language":"en-US","text":"English"}]}]}`
	plan := func(notesDir string) *orchestrator.PromotePlan {
		rt := newPromoteFake(promoteAPI{
			editID:             "edit-notes",
			sourceTrackGetResp: source,
			destTrack:          "production",
			destTrackGetResp:   productionHaltedAndDraft,
		})
		result, err := orchestrator.Promote(context.Background(), &http.Client{Transport: rt}, orchestrator.PromoteOpts{
			Package: "com.example.app", FromTrack: "beta", ToTrack: "production", ReleaseNotesDir: notesDir, Plan: true,
		})
		if err != nil {
			t.Fatalf("Promote --plan (notes dir %q): %v", notesDir, err)
		}
		for _, c := range apiCalls(rt) {
			if strings.HasPrefix(c, "PUT ") || strings.HasSuffix(c, ":commit") {
				t.Errorf("plan wrote: %s", c)
			}
		}
		return result.Promotion
	}
	if with, without := plan(dir), plan(""); !reflect.DeepEqual(with, without) {
		t.Errorf("plan with a complete override = %+v, want it equal to the plan without one %+v", with, without)
	}
}
