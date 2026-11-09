package orchestrator_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// A pinned explicit Edit is read in place: the plan sees the changes staged
// in it, and neither opens nor discards an Edit of its own.
func TestComplete_plan_readsInsideThePinnedEdit(t *testing.T) {
	rt := newPromoteFake(stateAPI{
		sourceTrackGetResp: `{"track":"beta","releases":[` +
			`{"name":"9","status":"completed","versionCodes":["9"]},` +
			`{"name":"10","status":"inProgress","versionCodes":["10"],"userFraction":0.5}]}`,
	})
	result, err := orchestrator.Complete(context.Background(), &http.Client{Transport: rt}, orchestrator.StateOpts{
		Package: "com.example.app", Track: "beta", VersionCode: 10, ExplicitEditID: "edit-pinned", Plan: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertSequence(t, apiCalls(rt), []string{"GET /androidpublisher/v3/applications/com.example.app/edits/edit-pinned/tracks/beta"})
	if result.Plan == nil || result.VersionCode != 10 || result.Status != "completed" {
		t.Fatalf("result = %+v, want a plan completing 10", result)
	}
	if got := result.SupersededVersionCodes; len(got) != 1 || got[0] != 9 {
		t.Errorf("superseded = %v, want [9]", got)
	}
	if len(result.Plan.Kept) != 0 || !strings.EqualFold(result.Plan.Target.Name, "10") {
		t.Errorf("plan = %+v, want target 10 and nothing kept", result.Plan)
	}
}
