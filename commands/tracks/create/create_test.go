// Package create_test exercises `gplay tracks create` at the kernel
// level: a RunContext built by hand, a testkit Fake injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote command's test harness so a single seam proves the auth +
// Edit lifecycle wiring for tracks create too.
package create_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/commands/tracks/create"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// createAPI is the Play API the create flow sees, served by a testkit Fake:
// edits.insert, tracks.create, edits.commit, edits.delete. trackCreateStatus
// lets a test force a non-2xx on the POST .../tracks (the "track already
// exists" case).
type createAPI struct {
	editID            string
	trackCreateStatus int
	trackCreateResp   string

	fake *testkit.Fake
}

func (a *createAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/tracks"):
			resp := a.trackCreateResp
			if resp == "" {
				resp = `{}`
			}
			return a.trackCreateStatus, resp, true
		case strings.HasSuffix(c.Path, ":commit"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, a.editID), true
		}
		return 0, "", false
	}, refuse(t, ""))
	return a.fake
}

// calls lists the requests as "METHOD path", the token exchanges first: the
// oauth2 transport runs the exchange before the first API call and caches
// the token, and the Fake counts exchanges without recording them.
func (a *createAPI) calls() []string {
	var out []string
	for range a.fake.TokenExchanges() {
		out = append(out, "POST /token")
	}
	for _, c := range a.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// trackCreateReq returns the body of the last tracks.create request.
func (a *createAPI) trackCreateReq() []byte {
	var body []byte
	for _, c := range a.fake.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/tracks") {
			body = c.Body
		}
	}
	return body
}

// refuse fails the test on any request the routes before it did not claim,
// as the hand-rolled transport's t.Fatalf did: on an error path Run's error
// alone would hide a stray request. The Fake still fails that round trip.
func refuse(t *testing.T, why string) testkit.Responder {
	t.Helper()
	return func(c testkit.Call) (int, string, bool) {
		t.Errorf("unexpected request%s: %s %s", why, c.Method, c.Path)
		return 0, "", false
	}
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
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
	return rc, &stdout
}

// TestRun_dryRun_noHTTP asserts --dry-run previews the TrackConfig
// without auth or any HTTP call, and the human renders mention the
// hardcoded CLOSED_TESTING type.
func TestRun_dryRun_noHTTP(t *testing.T) {
	api := &createAPI{}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on dry-run")
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", api.calls())
	}

	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(table.String(), "CLOSED_TESTING") {
		t.Errorf("table output = %q, want it to mention CLOSED_TESTING", table.String())
	}
}

// TestRun_happyPath_createsClosedTrack asserts the full vertical slice:
// /token exchange, then the open → create → commit sequence, with the
// create request carrying type=CLOSED_TESTING, and the --output json
// render being the raw tracks.create response verbatim (ADR-0003).
func TestRun_happyPath_createsClosedTrack(t *testing.T) {
	api := &createAPI{
		editID:          "edit-create-cli",
		trackCreateResp: `{"track":"qa-team","releases":[]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if api.fake.TokenExchanges() == 0 {
		t.Errorf("the fake saw no /token exchange; calls=%v", api.calls())
	}
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-create-cli/tracks",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-create-cli:commit",
	}
	if len(api.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(api.calls()), api.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if api.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, api.calls()[i], want)
		}
	}

	if !strings.Contains(string(api.trackCreateReq()), `"type":"CLOSED_TESTING"`) {
		t.Errorf("create request body = %s, want type=CLOSED_TESTING", api.trackCreateReq())
	}

	// ADR-0003: --output json must be the raw tracks.create response.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.trackCreateResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.create payload = %s", got, api.trackCreateResp)
	}
}

// TestRun_createOnExistingTrack_exit30 asserts that creating a track
// that already exists surfaces the API 400 verbatim (exit 30 via
// StatusToExitCode) (gplay does not fake idempotency) and that the
// failed Edit is auto-discarded (DELETE) since keep-edit-on-failure is
// off by default.
func TestRun_createOnExistingTrack_exit30(t *testing.T) {
	api := &createAPI{
		editID:            "edit-create-cli",
		trackCreateStatus: 400,
		trackCreateResp:   `{"error":{"code":400,"message":"Track already exists.","errors":[{"reason":"badRequest"}]}}`,
	}
	rc, _ := newRC(t, api.serve(t))

	_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"})
	if err == nil {
		t.Fatal("Run returned nil error; want the API 400 surfaced")
	}
	if got := exit.For(err); got != 30 {
		t.Errorf("exit.For(err) = %d, want 30; err=%v", got, err)
	}

	// The Edit must have been discarded after the failure.
	sawDelete := false
	for _, c := range api.calls() {
		if strings.HasPrefix(c, "DELETE ") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("expected an edits.delete (auto-discard) after the failure, calls=%v", api.calls())
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: the expected flags exist, the deliberately-absent ones
// (--type / --form-factor / --confirm) do not, and Use carries the
// positional <name>.
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := create.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"dry-run",
		"keep-edit-on-failure",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	for _, name := range []string{"type", "form-factor", "confirm"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("cobra command has unexpected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "create <name>" {
		t.Errorf("cmd.Use = %q, want %q", got, "create <name>")
	}
}

// TestRun_happyPath_emitsConfirmationOnStderr asserts a committed track create
// prints a single ✓ line on stderr (DESIGN §8) naming the new track, alongside
// the stdout payload.
func TestRun_happyPath_emitsConfirmationOnStderr(t *testing.T) {
	api := &createAPI{
		editID:          "edit-create-cli",
		trackCreateResp: `{"track":"qa-team","releases":[]}`,
	}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "qa-team") {
		t.Errorf("track create ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	api := &createAPI{}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team", DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}

// TestRun_forwardsCommitOptIns: an implicit-mode write command hands the #598
// opt-ins to its own edits.commit. The other tests here run with the flags
// unset, which sends no query (pinned in internal/play/edits).
func TestRun_forwardsCommitOptIns(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit-1"}`, true
		case strings.HasSuffix(c.Path, "/tracks"):
			return http.StatusOK, `{"track":"qa-team"}`, true
		case strings.HasSuffix(c.Path, ":commit"):
			return http.StatusOK, `{"id":"edit-1"}`, true
		}
		return 0, "", false
	})
	rc, _ := newRC(t, fake)

	in := create.Input{Package: "com.example.app", Name: "qa-team", Commit: commitflags.Flags{ChangesNotSentForReview: true}}
	if _, err := create.Run(rc, in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var commitQuery string
	for _, c := range fake.Calls() {
		if strings.HasSuffix(c.Path, ":commit") {
			commitQuery = c.Query
		}
	}
	if commitQuery != "changesNotSentForReview=true" {
		t.Errorf("commit query = %q, want changesNotSentForReview=true", commitQuery)
	}
}
