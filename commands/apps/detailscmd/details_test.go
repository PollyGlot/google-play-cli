package detailscmd_test

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

	"github.com/PollyGlot/google-play-cli/commands/apps/detailscmd"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type readAPI struct {
	editID  string
	details string

	detailsCode int

	fake *testkit.Fake
}

func (a *readAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/details"):
			return a.detailsCode, a.details, true
		}
		return 0, "", false
	}, testkit.Refuse(t, " (apps details is read-only, single-endpoint)"))
	return a.fake
}

func (a *readAPI) calls() []string {
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
	body := `{"contactEmail":"hi@example.com","contactPhone":"+1 555 0100","contactWebsite":"https://x.example","defaultLanguage":"en-US"}`
	api := &readAPI{editID: "edit-details", details: body}
	rc, _ := newRC(t, api.serve(t))

	r, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-details/details",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-details",
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
		t.Errorf("JSON output = %s\nwant details.get body verbatim = %s", got, body)
	}
}

func TestRun_usesPin_whenNoFlag(t *testing.T) {
	api := &readAPI{
		editID:  "edit-pin",
		details: `{"defaultLanguage":"fr-FR","contactEmail":"bonjour@example.fr"}`,
	}
	rc, _ := newRC(t, api.serve(t))
	rc.Resolved.Pin = "com.pinned.app"

	if _, err := detailscmd.Run(rc, detailscmd.Input{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range api.calls() {
		if strings.Contains(c, "com.pinned.app") {
			return
		}
	}
	t.Errorf("expected calls scoped to com.pinned.app, got: %v", api.calls())
}

func TestRun_missingPackage_exit2(t *testing.T) {
	api := &readAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := detailscmd.Run(rc, detailscmd.Input{})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	api := &readAPI{}
	rc, _ := newRC(t, api.serve(t))
	rc.Account = nil
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", api.calls())
	}
}

func TestRun_get403_exit11_apiAccessHint(t *testing.T) {
	api := &readAPI{
		editID:      "edit-403",
		detailsCode: 403,
		details:     `{"error":{"code":403,"message":"insufficient permissions"}}`,
	}
	rc, _ := newRC(t, api.serve(t))
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("403 error message = %q, want it to mention 'API access'", err.Error())
	}
}

func TestRun_get404_exit30_appsListHint(t *testing.T) {
	api := &readAPI{
		editID:      "edit-404",
		detailsCode: 404,
		details:     `{"error":{"code":404,"message":"app not found"}}`,
	}
	rc, _ := newRC(t, api.serve(t))
	_, err := detailscmd.Run(rc, detailscmd.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("404 error message = %q, want it to mention 'gplay apps list'", err.Error())
	}
}

func TestRenderTable_showsAllFourFields(t *testing.T) {
	p := detailscmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		ContactEmail:    "hi@example.com",
		ContactPhone:    "+1 555 0100",
		ContactWebsite:  "https://x.example",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "hi@example.com", "+1 555 0100", "https://x.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMarkdown_showsAllFourFields(t *testing.T) {
	p := detailscmd.Payload{
		Package:         "com.example.app",
		DefaultLanguage: "en-US",
		ContactEmail:    "hi@example.com",
		ContactPhone:    "+1 555 0100",
		ContactWebsite:  "https://x.example",
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"en-US", "hi@example.com", "+1 555 0100", "https://x.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

func TestGroup_bareDetailsIsPureNoun(t *testing.T) {
	group := detailscmd.NewCommand(kernel.Boot{})
	if group.Run != nil {
		t.Error("bare `apps details` must not carry a business Run (it is a grouping noun)")
	}
	if group.RunE == nil {
		t.Fatal("bare `apps details` must have the grouping RunE (help when bare, loud on unknown subcommand)")
	}
	group.SetOut(io.Discard)
	if err := group.RunE(group, nil); err != nil {
		t.Errorf("bare `apps details` should print help and succeed, got err=%v", err)
	}
	if err := group.RunE(group, []string{"nonesuch"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown subcommand of `apps details` should be rejected naming the command; got err=%v", err)
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
		t.Error("`apps details` must hold a `view` subcommand (the record read)")
	}
	if !hasSet {
		t.Error("`apps details` must keep its `set` subcommand (the write)")
	}
}

func TestViewCommand_hasReadSurface(t *testing.T) {
	view := detailscmd.NewViewCommand(kernel.Boot{})
	if view.RunE == nil {
		t.Error("`apps details view` must have a RunE (it reads the record)")
	}
	if view.Flags().Lookup("package") == nil {
		t.Error("`apps details view` must expose --package")
	}
	out := view.Flags().Lookup("output")
	if out == nil {
		t.Fatal("`apps details view` must expose --output")
	}
	for _, want := range []string{"table", "json", "markdown"} {
		if !strings.Contains(out.Usage, want) {
			t.Errorf("--output usage missing %q; got %q", want, out.Usage)
		}
	}
}
