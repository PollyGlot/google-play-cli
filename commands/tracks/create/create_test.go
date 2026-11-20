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
	}, testkit.Refuse(t, ""))
	return a.fake
}

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

func (a *createAPI) trackCreateReq() []byte {
	var body []byte
	for _, c := range a.fake.Calls() {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/tracks") {
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

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.trackCreateResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.create payload = %s", got, api.trackCreateResp)
	}
}

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

func TestNewCommand_registersExpectedFlags(t *testing.T) {
	cmd := create.NewCommand(kernel.Boot{})
	for _, name := range []string{
		"package",
		"dry-run",
		"keep-edit-on-failure",
		"form-factor",
		"output",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("cobra command missing expected flag --%s", name)
		}
	}
	if f := cmd.Flags().Lookup("form-factor"); f != nil && f.DefValue != "default" {
		t.Errorf("--form-factor default = %q, want %q", f.DefValue, "default")
	}
	for _, name := range []string{"type", "confirm"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("cobra command has unexpected flag --%s", name)
		}
	}
	if got := cmd.Use; got != "create <name>" {
		t.Errorf("cmd.Use = %q, want %q", got, "create <name>")
	}
}

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

func TestRun_dryRun_formFactorWear(t *testing.T) {
	api := &createAPI{}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "beta-wear", FormFactor: "Wear", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls on --dry-run, saw: %v", api.calls())
	}
	var out bytes.Buffer
	if err := r.Renderers().JSON(&out); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(out.String(), `"formFactor": "WEAR"`) {
		t.Errorf("dry-run JSON = %s, want formFactor WEAR", out.String())
	}
	if !strings.Contains(out.String(), `"track": "wear:beta-wear"`) {
		t.Errorf("dry-run JSON = %s, want the prefixed track wear:beta-wear", out.String())
	}
}

func TestRun_formFactorAutomotive_sentInBody(t *testing.T) {
	api := &createAPI{
		editID:          "edit-create-cli",
		trackCreateResp: `{"track":"automotive:beta-car","releases":[]}`,
	}
	rc, _ := newRC(t, api.serve(t))

	r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "beta-car", FormFactor: "automotive"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := string(api.trackCreateReq()); !strings.Contains(got, `"formFactor":"AUTOMOTIVE"`) {
		t.Errorf("create request body = %s, want formFactor=AUTOMOTIVE", got)
	}
	var table bytes.Buffer
	if err := r.Renderers().Table(&table); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(table.String(), "formFactor:  AUTOMOTIVE") {
		t.Errorf("table output = %q, want formFactor AUTOMOTIVE", table.String())
	}
	if !strings.Contains(table.String(), "track:       automotive:beta-car") {
		t.Errorf("table output = %q, want the prefixed track automotive:beta-car", table.String())
	}
}

func TestRun_helpExample_formFactorTrackIsPrefixed(t *testing.T) {
	for _, name := range []string{"beta-wear", "wear:beta-wear"} {
		api := &createAPI{editID: "edit-create-cli", trackCreateResp: `{"track":"wear:beta-wear","releases":[]}`}
		rc, _ := newRC(t, api.serve(t))

		r, err := create.Run(rc, create.Input{Package: "com.example.app", Name: name, FormFactor: "wear"})
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		want := `{"track":"wear:beta-wear","type":"CLOSED_TESTING","formFactor":"WEAR"}`
		if got := strings.TrimSpace(string(api.trackCreateReq())); got != want {
			t.Errorf("%s: create request body = %s, want %s", name, got, want)
		}
		var md bytes.Buffer
		if err := r.Renderers().Markdown(&md); err != nil {
			t.Fatalf("%s: Markdown render: %v", name, err)
		}
		if !strings.Contains(md.String(), "- **track**: wear:beta-wear\n") {
			t.Errorf("%s: markdown output = %q, want track wear:beta-wear", name, md.String())
		}
	}
}

func TestRun_contradictoryPrefix_exit2NoHTTP(t *testing.T) {
	for _, tc := range []struct{ name, formFactor string }{
		{"automotive:beta", "wear"},
		{"wear:beta", "automotive"},
		{"wear:beta", "default"},
		{"wear:", "wear"},
	} {
		for _, dryRun := range []bool{false, true} {
			api := &createAPI{editID: "edit-create-cli"}
			rc, _ := newRC(t, api.serve(t))

			_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: tc.name, FormFactor: tc.formFactor, DryRun: dryRun})
			if got := exit.For(err); got != 2 {
				t.Fatalf("%s --form-factor %s dryRun=%v: exit.For(err) = %d, want 2; err=%v", tc.name, tc.formFactor, dryRun, got, err)
			}
			if calls := api.calls(); len(calls) != 0 {
				t.Errorf("%s --form-factor %s dryRun=%v: expected zero HTTP calls, saw: %v", tc.name, tc.formFactor, dryRun, calls)
			}
		}
	}
}

func TestRun_defaultFormFactor_bodyUnchanged(t *testing.T) {
	api := &createAPI{editID: "edit-create-cli", trackCreateResp: `{"track":"qa-team","releases":[]}`}
	rc, _ := newRC(t, api.serve(t))

	if _, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "qa-team"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := `{"track":"qa-team","type":"CLOSED_TESTING","formFactor":"DEFAULT"}`
	if got := strings.TrimSpace(string(api.trackCreateReq())); got != want {
		t.Errorf("create request body = %s, want %s", got, want)
	}
}

func TestRun_unknownFormFactor_exit2NoHTTP(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		api := &createAPI{editID: "edit-create-cli"}
		rc, _ := newRC(t, api.serve(t))

		_, err := create.Run(rc, create.Input{Package: "com.example.app", Name: "beta-tv", FormFactor: "tv", DryRun: dryRun})
		if got := exit.For(err); got != 2 {
			t.Fatalf("dryRun=%v: exit.For(err) = %d, want 2; err=%v", dryRun, got, err)
		}
		for _, v := range []string{"default", "wear", "automotive"} {
			if !strings.Contains(err.Error(), v) {
				t.Errorf("dryRun=%v: error %q does not name accepted value %q", dryRun, err, v)
			}
		}
		if calls := api.calls(); len(calls) != 0 {
			t.Errorf("dryRun=%v: expected zero HTTP calls, saw: %v", dryRun, calls)
		}
	}
}
