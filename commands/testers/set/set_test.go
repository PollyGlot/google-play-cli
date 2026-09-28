// Package set_test exercises `gplay testers set` at the kernel level: a
// RunContext built by hand, a testkit Fake injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote / upload write-command harness so a single seam proves the auth +
// Edit lifecycle wiring (open → testers.update → commit) for testers set.
package set_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/testers/set"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// setAPI is the Play API a testers replacement sees, served by a testkit
// Fake: edits.insert, testers.update (PUT, body recorded), edits.commit, and
// edits.delete (the failure/discard path).
type setAPI struct {
	editID            string
	testersUpdateResp string

	fake *testkit.Fake
}

func (a *setAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/testers/"):
			resp := a.testersUpdateResp
			if resp == "" {
				resp = `{"googleGroups":[]}`
			}
			return 200, resp, true
		case strings.HasSuffix(c.Path, ":commit"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, a.editID), true
		}
		return 0, "", false
	}, testkit.Refuse(t, ""))
	return a.fake
}

// calls lists the requests as "METHOD path", the token exchanges first: the
// oauth2 transport runs the exchange before the first API call and caches
// the token, and the Fake counts exchanges without recording them.
func (a *setAPI) calls() []string {
	var out []string
	for range a.fake.TokenExchanges() {
		out = append(out, "POST /token")
	}
	for _, c := range a.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// testersUpdateReq returns the body of the last testers.update request.
func (a *setAPI) testersUpdateReq() []byte {
	var body []byte
	for _, c := range a.fake.Calls() {
		if c.Method == http.MethodPut && strings.Contains(c.Path, "/testers/") {
			body = c.Body
		}
	}
	return body
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

// TestRun_bareSet_exit2_noHTTP asserts the footgun guard: a bare `set`
// with neither --group nor --clear is misuse: it must short-circuit with
// exit 2 before any HTTP, so a forgotten --group can never silently wipe
// the list.
func TestRun_bareSet_exit2_noHTTP(t *testing.T) {
	api := &setAPI{}
	rc, _ := newRC(t, api.serve(t))

	_, err := set.Run(rc, set.Input{Package: "com.example.app", Track: "qa-team"})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

// TestRun_groupAndClear_exit2 asserts --group and --clear together is
// contradictory: exit 2 before any HTTP.
func TestRun_groupAndClear_exit2(t *testing.T) {
	api := &setAPI{}
	rc, _ := newRC(t, api.serve(t))

	_, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com"},
		GroupsSet: true,
		Clear:     true,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	if got := exit.For(err); got != 2 {
		t.Errorf("exit.For(err) = %d, want 2; err=%v", got, err)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

// TestRun_clear_putsEmptyArray asserts --clear writes an empty audience:
// the full open → PUT → commit sequence runs and the captured PUT body
// carries an explicit empty array (NOT null).
func TestRun_clear_putsEmptyArray(t *testing.T) {
	api := &setAPI{
		editID:            "edit-clear",
		testersUpdateResp: `{"googleGroups":[]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := set.Run(rc, set.Input{
		Package: "com.example.app",
		Track:   "qa-team",
		Clear:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --clear")
	}

	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-clear/testers/qa-team",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-clear:commit",
	}
	if len(api.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(api.calls()), api.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if api.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, api.calls()[i], want)
		}
	}
	body := string(api.testersUpdateReq())
	if !strings.Contains(body, `"googleGroups":[]`) {
		t.Errorf("testers.update body = %s, want it to contain \"googleGroups\":[]", body)
	}
}

// TestRun_setGroups_putsList asserts `--group a,b` writes [a,b]: the PUT
// body carries both groups and --output json is the raw testers.update
// response (ADR-0003 pass-through).
func TestRun_setGroups_putsList(t *testing.T) {
	api := &setAPI{
		editID:            "edit-set",
		testersUpdateResp: `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com", "b@googlegroups.com"},
		GroupsSet: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --group set")
	}

	body := string(api.testersUpdateReq())
	if !strings.Contains(body, "a@googlegroups.com") || !strings.Contains(body, "b@googlegroups.com") {
		t.Errorf("testers.update body = %s, want both groups", body)
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.testersUpdateResp) {
		t.Errorf("JSON output = %s\nwant raw testers.update payload = %s", got, api.testersUpdateResp)
	}
}

// TestRun_dryRun_noHTTP asserts --dry-run previews the target audience
// without any HTTP call and still returns a non-nil Renderable whose view
// shows the groups that would be written.
func TestRun_dryRun_noHTTP(t *testing.T) {
	api := &setAPI{}
	rc, _ := newRC(t, api.serve(t))

	r, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com", "b@googlegroups.com"},
		GroupsSet: true,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on --dry-run")
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", api.calls())
	}

	var tableOut bytes.Buffer
	if err := r.Renderers().Table(&tableOut); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := tableOut.String()
	if !strings.Contains(out, "a@googlegroups.com") || !strings.Contains(out, "b@googlegroups.com") {
		t.Errorf("dry-run preview = %q, want both target groups", out)
	}
}

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: every write flag exists, there is NO --confirm, and the
// command is named "set".
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := set.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"track",
		"group",
		"clear",
		"dry-run",
		"keep-edit-on-failure",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if cmd.Flags().Lookup("confirm") != nil {
		t.Error("cobra command has a --confirm flag; testers set must NOT have one")
	}
	if got := cmd.Use; got != "set" {
		t.Errorf("cmd.Use = %q, want %q", got, "set")
	}
}

// TestRun_setGroups_emitsConfirmationOnStderr asserts a committed testers set
// prints a single ✓ line on stderr (DESIGN §8) naming the track, alongside the
// stdout payload.
func TestRun_setGroups_emitsConfirmationOnStderr(t *testing.T) {
	api := &setAPI{
		editID:            "edit-set",
		testersUpdateResp: `{"googleGroups":["a@googlegroups.com","b@googlegroups.com"]}`,
	}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com", "b@googlegroups.com"},
		GroupsSet: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "qa-team") {
		t.Errorf("testers set ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	api := &setAPI{}
	rc, _ := newRC(t, api.serve(t))
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := set.Run(rc, set.Input{
		Package:   "com.example.app",
		Track:     "qa-team",
		Groups:    []string{"a@googlegroups.com"},
		GroupsSet: true,
		DryRun:    true,
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
