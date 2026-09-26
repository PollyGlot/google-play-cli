// Package list_test exercises `gplay testers list` at the kernel level: a
// RunContext built by hand, a testkit Fake injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// tracks view / promote harness so a single seam proves the auth +
// read-only Edit lifecycle wiring for testers list too.
package list_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/testers/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// listAPI is the Play API a read-only testers listing sees, served by a
// testkit Fake: edits.insert, testers.get, edits.delete (the read-only
// discard).
type listAPI struct {
	editID         string
	testersGetResp string

	fake *testkit.Fake
}

func (a *listAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/testers/"):
			return 200, a.testersGetResp, true
		}
		return 0, "", false
	}, refuse(t, ""))
	return a.fake
}

// calls lists the requests as "METHOD path", the token exchanges first: the
// oauth2 transport runs the exchange before the first API call and caches
// the token, and the Fake counts exchanges without recording them.
func (a *listAPI) calls() []string {
	var out []string
	for range a.fake.TokenExchanges() {
		out = append(out, "POST /token")
	}
	for _, c := range a.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
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

// TestRun_happyPath_listsGroups asserts the full read-only vertical slice:
// /token exchange precedes edits.insert, then testers.get reads the
// audience, then the Edit is discarded (edits.delete): never committed.
// --output json must be the raw testers.get body verbatim (ADR-0003).
func TestRun_happyPath_listsGroups(t *testing.T) {
	api := &listAPI{
		editID:         "edit-testers-cli",
		testersGetResp: `{"googleGroups":["qa@googlegroups.com","beta@googlegroups.com"]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := list.Run(rc, list.Input{
		Package: "com.example.app",
		Track:   "qa-team",
	})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-testers-cli/testers/qa-team",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-testers-cli",
	}
	if len(api.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(api.calls()), api.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if api.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, api.calls()[i], want)
		}
	}

	// ADR-0003: --output json must be API pass-through: the raw
	// testers.get response body verbatim.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.testersGetResp) {
		t.Errorf("JSON output = %s\nwant raw testers.get payload = %s", got, api.testersGetResp)
	}
}

// TestRun_missingTrack_exit2_noHTTP asserts --track is required: the CLI
// must short-circuit with a usage error (exit 2) before any HTTP call.
func TestRun_missingTrack_exit2_noHTTP(t *testing.T) {
	api := &listAPI{}
	rc, _ := newRC(t, api.serve(t))

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
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

// TestNewCommand_registersExpectedFlags is a thin smoke test for the
// cobra wiring: --package, --track, --output exist and the command is
// named "list".
func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := list.NewCommand(kernel.Boot{})
	for _, name := range []string{"package", "track", "output"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "list" {
		t.Errorf("cmd.Use = %q, want %q", got, "list")
	}
}
