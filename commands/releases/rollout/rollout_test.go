package rollout_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/rollout"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type stateAPI struct {
	editID             string
	trackGetResp       string
	trackUpdateRawResp string
}

func newStateFake(a stateAPI) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			return http.StatusOK, a.trackGetResp, true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/"):
			if a.trackUpdateRawResp == "" {
				return http.StatusOK, `{}`, true
			}
			return http.StatusOK, a.trackUpdateRawResp, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, a.editID), true
		}
		return 0, "", false
	})
}

func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func trackUpdateReq(f *testkit.Fake) []byte {
	var body []byte
	for _, c := range f.Calls() {
		if c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/") {
			body = c.Body
		}
	}
	return body
}

func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
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
	return rc
}

const oneInProgressRelease = `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.05}]}`

func TestRunRollout_happyPath_fullSequence(t *testing.T) {
	api := stateAPI{
		editID:             "edit-rollout-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2}]}`,
	}
	rt := newStateFake(api)
	rc := newRC(t, rt)

	r, err := rollout.RunRollout(rc, rollout.Input{
		Package:           "com.example.app",
		Track:             "production",
		StagedFraction:    0.2,
		StagedFractionSet: true,
		Confirm:           true,
	})
	if err != nil {
		t.Fatalf("RunRollout: %v", err)
	}
	if r == nil {
		t.Fatal("RunRollout returned nil Renderable on happy path")
	}
	if rt.TokenExchanges() == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", apiCalls(rt))
	}

	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli/tracks/production",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli/tracks/production",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-rollout-cli:commit",
	}
	calls := apiCalls(rt)
	if len(calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(calls), calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls[i], want)
		}
	}

	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"inProgress"`) || !strings.Contains(body, `"userFraction":0.2`) {
		t.Errorf("tracks.update body = %s, want inProgress at 0.2", body)
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.trackUpdateRawResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.update payload = %s", got, api.trackUpdateRawResp)
	}
}

func TestRunHalt_happyPath(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-halt-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
	})
	rc := newRC(t, rt)

	if _, err := rollout.RunHalt(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
	}); err != nil {
		t.Fatalf("RunHalt: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"halted"`) {
		t.Errorf("tracks.update body = %s, want status=halted", body)
	}
}

func TestRunResume_happyPath(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-resume-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
		trackUpdateRawResp: `{}`,
	})
	rc := newRC(t, rt)

	if _, err := rollout.RunResume(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	}); err != nil {
		t.Fatalf("RunResume: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"inProgress"`) || !strings.Contains(body, `"userFraction":0.05`) {
		t.Errorf("tracks.update body = %s, want inProgress at preserved 0.05", body)
	}
}

func TestRunComplete_happyPath(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-complete-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{}`,
	})
	rc := newRC(t, rt)

	if _, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
		Confirm: true,
	}); err != nil {
		t.Fatalf("RunComplete: %v", err)
	}
	body := string(trackUpdateReq(rt))
	if !strings.Contains(body, `"status":"completed"`) || strings.Contains(body, `"userFraction"`) {
		t.Errorf("tracks.update body = %s, want completed with no fraction", body)
	}
}

func TestRun_productionWriteWithoutConfirm_returnsExit3(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
	})
	assertExit(t, err, 3)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before confirm guard, saw: %v", apiCalls(rt))
	}
}

func TestRunRollout_missingStaged_returnsExit2(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package: "com.example.app",
		Track:   "production",
	})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
	if !strings.Contains(err.Error(), "--staged") {
		t.Errorf("err = %q, want it to mention --staged", err.Error())
	}
}

func TestRunRollout_outOfRangeStaged_returnsExit2(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package:           "com.example.app",
		Track:             "production",
		StagedFraction:    1.5,
		StagedFractionSet: true,
	})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRunRollout_stagedOne_pointsAtComplete(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunRollout(rc, rollout.Input{
		Package:           "com.example.app",
		Track:             "production",
		StagedFraction:    1.0,
		StagedFractionSet: true,
		Confirm:           true,
	})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
	if !strings.Contains(err.Error(), "gplay releases complete") {
		t.Errorf("err = %q, want it to point at gplay releases complete", err.Error())
	}
}

func TestRun_missingTrack_returnsExit2(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunComplete(rc, rollout.Input{
		Package: "com.example.app",
	})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRun_missingPackage_returnsExit2(t *testing.T) {
	rt := newStateFake(stateAPI{})
	rc := newRC(t, rt)

	_, err := rollout.RunHalt(rc, rollout.Input{
		Track: "production",
	})
	assertExit(t, err, 2)
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRenderTable_showsUserFractionForHalted(t *testing.T) {
	p := rollout.Payload{Result: &orchestrator.Result{
		VersionCode:  142,
		Track:        "production",
		Status:       "halted",
		UserFraction: 0.05,
		ReleaseName:  "142",
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "userFraction") || !strings.Contains(out, "0.05") {
		t.Errorf("table output = %q, want a userFraction row showing 0.05 for halted", out)
	}
	if !strings.Contains(out, "halted") {
		t.Errorf("table output = %q, want the halted status", out)
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != want {
		t.Errorf("ExitCode() = %d, want %d", coder.ExitCode(), want)
	}
}

func confirmStderr(t *testing.T, rt http.RoundTripper, fn func(rc *kernel.RunContext) error) string {
	t.Helper()
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr
	if err := fn(rc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return stderr.String()
}

func TestRunRollout_emitsConfirmationWithUserFractionPercent(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-rollout-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.01}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2}]}`,
	})
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", StagedFraction: 0.2, StagedFractionSet: true, Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("stderr missing ✓ confirmation line:\n%s", got)
	}
	for _, want := range []string{"production", "20%"} {
		if !strings.Contains(got, want) {
			t.Errorf("✓ line %q missing %q", got, want)
		}
	}
}

func TestRunHalt_emitsConfirmation(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-halt-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
	})
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunHalt(rc, rollout.Input{Package: "com.example.app", Track: "production"})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "halted") || !strings.Contains(got, "production") {
		t.Errorf("halt ✓ line wrong:\n%s", got)
	}
}

func TestRunResume_emitsConfirmation(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-resume-cli",
		trackGetResp:       `{"track":"production","releases":[{"name":"142","status":"halted","versionCodes":["142"],"userFraction":0.05}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.05}]}`,
	})
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunResume(rc, rollout.Input{Package: "com.example.app", Track: "production", Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "resumed") || !strings.Contains(got, "production") {
		t.Errorf("resume ✓ line wrong:\n%s", got)
	}
}

func TestRunComplete_emitsConfirmation(t *testing.T) {
	rt := newStateFake(stateAPI{
		editID:             "edit-complete-cli",
		trackGetResp:       oneInProgressRelease,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunComplete(rc, rollout.Input{Package: "com.example.app", Track: "production", Confirm: true})
		return err
	})
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "completed") || !strings.Contains(got, "production") {
		t.Errorf("complete ✓ line wrong:\n%s", got)
	}
}

func TestRunComplete_reportsTheSupersededRelease(t *testing.T) {
	const updated = `{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"]}]}`
	rt := newStateFake(stateAPI{
		editID: "edit-supersede-cli",
		trackGetResp: `{"track":"production","releases":[` +
			`{"name":"141","status":"completed","versionCodes":["141"]},` +
			`{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.2}]}`,
		trackUpdateRawResp: updated,
	})
	var payload output.Renderable
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		var err error
		payload, err = rollout.RunComplete(rc, rollout.Input{Package: "com.example.app", Track: "production", VersionCode: 142, Confirm: true})
		return err
	})
	if !strings.Contains(got, "superseded versionCode 141") {
		t.Errorf("✓ line %q, want it to name the superseded versionCode 141", got)
	}
	if body := string(trackUpdateReq(rt)); strings.Contains(body, `"141"`) {
		t.Errorf("tracks.update body = %s, want release 141 dropped", body)
	}

	var table, js bytes.Buffer
	if err := output.Render(&table, output.FormatTable, payload.Renderers()); err != nil {
		t.Fatalf("table render: %v", err)
	}
	if !strings.Contains(table.String(), "superseded:   141") {
		t.Errorf("table = %q, want a superseded: 141 line", table.String())
	}
	if err := output.Render(&js, output.FormatJSON, payload.Renderers()); err != nil {
		t.Fatalf("json render: %v", err)
	}
	if strings.TrimSpace(js.String()) != updated {
		t.Errorf("json = %s, want the tracks.update body verbatim %s", js.String(), updated)
	}
}

func TestRunRollout_dryRun_noConfirmation(t *testing.T) {
	rt := newStateFake(stateAPI{editID: "edit-dry", trackGetResp: oneInProgressRelease})
	got := confirmStderr(t, rt, func(rc *kernel.RunContext) error {
		_, err := rollout.RunRollout(rc, rollout.Input{Package: "com.example.app", Track: "production", StagedFraction: 0.2, StagedFractionSet: true, DryRun: true})
		return err
	})
	if strings.Contains(got, "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", got)
	}
}
