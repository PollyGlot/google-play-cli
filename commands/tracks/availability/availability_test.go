package availability_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/tracks/availability"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type availAPI struct {
	editID string
	body   string
	code   int

	fake *testkit.Fake
}

func (a *availAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/countryAvailability/"):
			return a.code, a.body, true
		}
		return 0, "", false
	}, testkit.Refuse(t, " (tracks availability is read-only)"))
	return a.fake
}

func (a *availAPI) calls() []string {
	var out []string
	for range a.fake.TokenExchanges() {
		out = append(out, "POST /token")
	}
	for _, c := range a.fake.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
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
	rc.Resolved = &config.Resolved{}
	return rc, &stdout
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	return coder.ExitCode()
}

func TestRun_happyPath(t *testing.T) {
	body := `{"syncWithProduction":false,"restOfWorld":true,"countries":[{"countryCode":"US"},{"countryCode":"GB"}]}`
	api := &availAPI{editID: "edit-avail", body: body}
	rc, _ := newRC(t, api.serve(t))

	r, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "production"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-avail/countryAvailability/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-avail",
	}
	if len(api.calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(api.calls()), api.calls(), len(wantSequence))
	}
	for i, want := range wantSequence {
		if api.calls()[i] != want {
			t.Errorf("call %d = %q, want %q", i, api.calls()[i], want)
		}
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(body) {
		t.Errorf("JSON output = %s\nwant body verbatim = %s", got, body)
	}
}

func TestRun_missingTrack_exit2_noHTTP(t *testing.T) {
	api := &availAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2 (missing --track)", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_missingPackage_exit2(t *testing.T) {
	api := &availAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := availability.Run(rc, availability.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_usesPin_whenNoFlag(t *testing.T) {
	api := &availAPI{
		editID: "edit-pin",
		body:   `{"syncWithProduction":true,"restOfWorld":false,"countries":[]}`,
	}
	rc, _ := newRC(t, api.serve(t))
	rc.Resolved.Pin = "com.pinned.app"

	if _, err := availability.Run(rc, availability.Input{Track: "beta"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range api.calls() {
		if strings.Contains(c, "com.pinned.app") {
			return
		}
	}
	t.Errorf("expected calls scoped to com.pinned.app, got: %v", api.calls())
}

func TestRun_get403_exit11(t *testing.T) {
	api := &availAPI{
		editID: "edit-403",
		code:   403,
		body:   `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, api.serve(t))
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
}

func TestRun_get404_exit30_tracksListHint(t *testing.T) {
	api := &availAPI{
		editID: "edit-404",
		code:   404,
		body:   `{"error":{"code":404,"message":"track not found"}}`,
	}
	rc, _ := newRC(t, api.serve(t))
	_, err := availability.Run(rc, availability.Input{Package: "com.example.app", Track: "no-such-track"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "gplay tracks list") {
		t.Errorf("404 error = %q, want it to mention 'gplay tracks list'", err.Error())
	}
}

func TestRenderTable_showsAvailabilityFields(t *testing.T) {
	p := availability.Payload{
		Track:              "production",
		SyncWithProduction: false,
		RestOfWorld:        true,
		Countries:          []string{"US", "GB", "FR"},
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"production", "US", "GB", "FR"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdown_showsAvailabilityFields(t *testing.T) {
	p := availability.Payload{
		Track:              "production",
		SyncWithProduction: true,
		RestOfWorld:        false,
		Countries:          []string{"US", "GB"},
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"production", "US", "GB"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

func TestGroup_bareAvailabilityIsPureNoun(t *testing.T) {
	group := availability.NewCommand(kernel.Boot{})
	if got := group.Use; got != "availability" {
		t.Errorf("group.Use = %q, want %q", got, "availability")
	}
	if group.Run != nil {
		t.Error("bare `tracks availability` must not carry a business Run (it is a grouping noun)")
	}
	if group.RunE == nil {
		t.Fatal("bare `tracks availability` must have the grouping RunE (help when bare, loud on unknown subcommand)")
	}
	group.SetOut(io.Discard)
	if err := group.RunE(group, nil); err != nil {
		t.Errorf("bare `tracks availability` should print help and succeed, got err=%v", err)
	}
	if err := group.RunE(group, []string{"nonesuch"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown subcommand of `tracks availability` should be rejected naming the command; got err=%v", err)
	}
	var hasView, hasSet bool
	for _, sub := range group.Commands() {
		switch sub.Name() {
		case "view":
			hasView = true
		case "set":
			hasSet = true
		}
	}
	if !hasView {
		t.Error("`tracks availability` must hold a `view` subcommand (the read)")
	}
	if hasSet {
		t.Error("`tracks availability` is read-only: it must not gain a `set`")
	}
}

func TestViewCommand_hasReadSurface(t *testing.T) {
	view := availability.NewViewCommand(kernel.Boot{})
	if view.RunE == nil {
		t.Error("`tracks availability view` must have a RunE (it reads the resource)")
	}
	for _, name := range []string{"package", "track", "output"} {
		if view.Flags().Lookup(name) == nil {
			t.Errorf("`tracks availability view` missing expected flag --%s", name)
		}
	}
}
