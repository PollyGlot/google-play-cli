package promote_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// sourceWithPriority3 is a beta track whose release carries a priority set in
// the Play Console.
const sourceWithPriority3 = `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"],"inAppUpdatePriority":3}]}`

// promotedPriority returns the raw inAppUpdatePriority of the single release
// the destination tracks.update PUT carried ("" when the key is absent).
func promotedPriority(t *testing.T, f *testkit.Fake) string {
	t.Helper()
	var body []byte
	for _, c := range f.Calls() {
		if c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/") {
			body = c.Body
		}
	}
	var track struct {
		Releases []map[string]json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(body, &track); err != nil || len(track.Releases) != 1 {
		t.Fatalf("tracks.update body = %s (err %v), want one release", body, err)
	}
	return string(track.Releases[0]["inAppUpdatePriority"])
}

// TestRun_promote_carriesSourceUpdatePriorityOver pins #664: without the flag
// the source release's priority reaches the destination instead of being
// dropped by the typed rebuild.
func TestRun_promote_carriesSourceUpdatePriorityOver(t *testing.T) {
	rt := newPromoteFake(promoteAPI{editID: "edit-carry", sourceTrackGetResp: sourceWithPriority3})
	rc, _ := newRC(t, rt)

	if _, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := promotedPriority(t, rt); got != "3" {
		t.Errorf("destination inAppUpdatePriority = %q, want 3 (carried over from the source)", got)
	}
}

// TestRun_promote_updatePriorityFlagOverridesSource: the flag wins over the
// source's value.
func TestRun_promote_updatePriorityFlagOverridesSource(t *testing.T) {
	rt := newPromoteFake(promoteAPI{editID: "edit-override", sourceTrackGetResp: sourceWithPriority3})
	rc, _ := newRC(t, rt)

	if _, err := promote.Run(rc, promote.Input{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha",
		UpdatePriority: 5, UpdatePrioritySet: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := promotedPriority(t, rt); got != "5" {
		t.Errorf("destination inAppUpdatePriority = %q, want 5 (the flag overrides the source's 3)", got)
	}
}

// TestRun_promote_sourceWithoutPriority_fieldStaysAbsent keeps today's
// payload unchanged when neither the source nor the flag sets a priority.
func TestRun_promote_sourceWithoutPriority_fieldStaysAbsent(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-none",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"]}]}`,
	})
	rc, _ := newRC(t, rt)

	if _, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := promotedPriority(t, rt); got != "" {
		t.Errorf("destination inAppUpdatePriority = %q, want the key absent", got)
	}
}

// TestRun_promote_dryRun_showsUpdatePriorityOverride: the preview shows the
// flag's value (the source's own priority needs tracks.get, which a dry-run
// never issues).
func TestRun_promote_dryRun_showsUpdatePriorityOverride(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	rc, _ := newRC(t, rt)

	r, err := promote.Run(rc, promote.Input{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha", DryRun: true,
		UpdatePriority: 2, UpdatePrioritySet: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode dry-run JSON: %v", err)
	}
	if string(got["inAppUpdatePriority"]) != "2" {
		t.Errorf("dry-run JSON = %s, want \"inAppUpdatePriority\": 2", buf.Bytes())
	}
	if touched(rt) {
		t.Errorf("dry-run reached the transport: %v", apiCalls(rt))
	}
}

// TestRun_promote_updatePriorityOutOfRange_exit2_noHTTP mirrors upload's
// guard: outside 0..5 is a usage error before any request.
func TestRun_promote_updatePriorityOutOfRange_exit2_noHTTP(t *testing.T) {
	rt := newPromoteFake(promoteAPI{editID: "edit-bad", sourceTrackGetResp: sourceWithPriority3})
	rc, _ := newRC(t, rt)

	_, err := promote.Run(rc, promote.Input{
		Package: "com.example.app", FromTrack: "beta", ToTrack: "alpha",
		UpdatePriority: 6, UpdatePrioritySet: true,
	})
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if touched(rt) {
		t.Errorf("RoundTripper saw calls on a usage error: %v", apiCalls(rt))
	}
}

// TestNewCommand_registersUpdatePriorityFlag pins the cobra wiring.
func TestNewCommand_registersUpdatePriorityFlag(t *testing.T) {
	if promote.NewCommand(kernel.Boot{}).Flags().Lookup("update-priority") == nil {
		t.Error("releases promote is missing --update-priority")
	}
}
