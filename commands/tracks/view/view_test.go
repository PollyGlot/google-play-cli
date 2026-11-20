package view_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/tracks/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type statusAPI struct {
	editID     string
	trackResp  string
	insertCode int
	trackCode  int
	insertBody string

	fake *testkit.Fake
}

func (a *statusAPI) serve(t *testing.T) *testkit.Fake {
	t.Helper()
	a.fake = testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			body := a.insertBody
			if body == "" {
				body = fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID)
			}
			return a.insertCode, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			return a.trackCode, a.trackResp, true
		}
		return 0, "", false
	}, testkit.Refuse(t, " (read-only status must not write/commit)"))
	return a.fake
}

func (a *statusAPI) calls() []string {
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

func TestRun_statusOfTrack_happyPath(t *testing.T) {
	raw := `{"track":"production","releases":[` +
		`{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0},` +
		`{"name":"143","status":"inProgress","versionCodes":["143"],"userFraction":0.1}` +
		`]}`
	api := &statusAPI{editID: "edit-status", trackResp: raw}
	rc, _ := newRC(t, api.serve(t))

	r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-status/tracks/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-status",
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
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(raw) {
		t.Errorf("JSON output = %s\nwant raw tracks.get payload = %s", got, raw)
	}
}

func TestRenderTable_halted_isVisuallyDistinct(t *testing.T) {
	cols, _ := view.ResolveColumns("status")
	p := view.Payload{
		Releases: []tracks.Release{
			{Name: "143", Status: "halted", VersionCodes: []string{"143"}, UserFraction: 0.1},
			{Name: "144", Status: "inProgress", VersionCodes: []string{"144"}, UserFraction: 0.2},
		},
		Columns: cols,
	}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "!HALTED") {
		t.Errorf("table missing halted marker !HALTED:\n%s", out)
	}
	if !strings.Contains(out, "inProgress") {
		t.Errorf("table missing plain inProgress status:\n%s", out)
	}
	if strings.Contains(out, "!INPROGRESS") {
		t.Errorf("inProgress wrongly marked as halted:\n%s", out)
	}
}

func lineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func TestRenderTable_listsEveryCoexistingRelease(t *testing.T) {
	p := view.Payload{
		Releases: []tracks.Release{
			{Name: "150-draft", Status: "draft", VersionCodes: []string{"150"}},
			{Name: "143", Status: "inProgress", VersionCodes: []string{"143"}, UserFraction: 0.1},
			{Name: "142", Status: "halted", VersionCodes: []string{"142"}, UserFraction: 0.05},
			{Name: "140-live", Status: "completed", VersionCodes: []string{"140"}, UserFraction: 1.0, ReleaseNotes: []tracks.LocalizedText{
				{Language: "en-US", Text: "Bug fixes"},
				{Language: "fr-FR", Text: "Corrections"},
			}},
		},
		Columns: mustDefaultColumns(t),
	}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"NAME", "STATUS", "USER_FRACTION", "VERSION_CODES", "NOTES_LOCALES"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing header %q:\n%s", want, out)
		}
	}
	for _, name := range []string{"150-draft", "143", "142", "140-live"} {
		if !strings.Contains(out, name) {
			t.Errorf("table missing release %q:\n%s", name, out)
		}
	}
	row := lineContaining(out, "140-live")
	fields := strings.Fields(row)
	if len(fields) == 0 || fields[len(fields)-1] != "2" {
		t.Errorf("completed release notes-locale count = %v, want last field 2:\nrow=%q", fields, row)
	}
}

func TestRun_derivesTrackKind(t *testing.T) {
	cases := map[string]string{
		"internal":   "standard",
		"alpha":      "standard",
		"beta":       "standard",
		"production": "standard",
		"qa-closed":  "custom",
	}
	for track, wantKind := range cases {
		t.Run(track, func(t *testing.T) {
			raw := `{"track":"` + track + `","releases":[{"name":"1","status":"completed","versionCodes":["1"]}]}`
			api := &statusAPI{editID: "edit-kind", trackResp: raw}
			rc, _ := newRC(t, api.serve(t))

			r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: track})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			var buf bytes.Buffer
			if err := r.Renderers().Table(&buf); err != nil {
				t.Fatalf("Table render: %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, track) {
				t.Errorf("table missing track name %q:\n%s", track, out)
			}
			if !strings.Contains(out, wantKind) {
				t.Errorf("table missing kind %q for track %q:\n%s", wantKind, track, out)
			}
		})
	}
}

func TestDefaultColumns_documentedOrder(t *testing.T) {
	want := []string{"name", "status", "userFraction", "versionCodes", "notes"}
	got := make([]string, 0, len(want))
	for _, c := range mustDefaultColumns(t) {
		got = append(got, c.Key)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default columns = %v, want %v", got, want)
	}
}

func mustDefaultColumns(t *testing.T) []output.Column[tracks.Release] {
	t.Helper()
	cols, err := view.ResolveColumns("")
	if err != nil {
		t.Fatalf("ResolveColumns(\"\"): %v", err)
	}
	return cols
}

func TestRun_columnsOverride_restrictsColumns(t *testing.T) {
	raw := `{"track":"production","releases":[{"name":"143","status":"inProgress","versionCodes":["143"],"userFraction":0.1}]}`
	api := &statusAPI{editID: "edit-cols", trackResp: raw}
	rc, _ := newRC(t, api.serve(t))

	r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production", Columns: "name,status"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing requested column %q:\n%s", want, out)
		}
	}
	for _, drop := range []string{"USER_FRACTION", "VERSION_CODES", "NOTES_LOCALES"} {
		if strings.Contains(out, drop) {
			t.Errorf("table shows column %q dropped by --columns override:\n%s", drop, out)
		}
	}
}

func TestRun_unknownColumn_exit2(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production", Columns: "name,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRenderMarkdown_isGFMTable_marksHalted(t *testing.T) {
	p := view.Payload{
		Track: "production",
		Kind:  "standard",
		Releases: []tracks.Release{
			{Name: "143", Status: "halted", VersionCodes: []string{"143"}, UserFraction: 0.1},
		},
		Columns: mustDefaultColumns(t),
	}

	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "143") {
		t.Errorf("markdown output = %q, want the release row", out)
	}
	if !strings.Contains(out, "!HALTED") {
		t.Errorf("markdown output = %q, want the halted marker !HALTED", out)
	}
	if !strings.Contains(out, "Track: production (standard)") {
		t.Errorf("markdown output = %q, want the track/kind context line", out)
	}
}

func TestRenderJSON_emptyRaw_errors(t *testing.T) {
	var buf bytes.Buffer
	if err := (view.Payload{}).Renderers().JSON(&buf); err == nil {
		t.Fatalf("JSON render of empty Raw = nil error, want a non-nil error")
	}
}

func TestRun_unknownTrack_exit30WithHint(t *testing.T) {
	api := &statusAPI{
		editID:    "edit-404",
		trackCode: 404,
		trackResp: `{"error":{"code":404,"message":"Track not found.","errors":[{"reason":"trackNotFound"}]}}`,
	}
	rc, _ := newRC(t, api.serve(t))

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "nope"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "tracks list") {
		t.Errorf("error %q, want a hint mentioning `gplay tracks list`", err.Error())
	}
	sawDelete := false
	for _, c := range api.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-404") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after an unknown-track read; calls = %v", api.calls())
	}
}

func TestRun_unknownPackage_exit30WithHint(t *testing.T) {
	api := &statusAPI{
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"The requested package does not exist."}}`,
	}
	rc, _ := newRC(t, api.serve(t))

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "apps list") {
		t.Errorf("error %q, want a hint mentioning `gplay apps list`", err.Error())
	}
	for _, c := range api.calls() {
		if strings.Contains(c, "/tracks/") {
			t.Errorf("track was read despite an insert (package) failure; calls = %v", api.calls())
		}
	}
}

func TestRun_forbidden_exit11WithHint(t *testing.T) {
	api := &statusAPI{
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	}
	rc, _ := newRC(t, api.serve(t))

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}

func TestRun_tracksGetError_discardsEditAndPropagates(t *testing.T) {
	api := &statusAPI{
		editID:    "edit-err",
		trackCode: 503,
		trackResp: `{"error":{"code":503,"message":"Backend error"}}`,
	}
	rc, _ := newRC(t, api.serve(t))

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 40 {
		t.Errorf("ExitCode() = %d, want 40", code)
	}
	sawDelete := false
	for _, c := range api.calls() {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-err") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after a failed read; calls = %v", api.calls())
	}
}

func TestRun_missingTrack_exit2(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := view.Run(rc, view.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_missingPackage_exit2(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := view.Run(rc, view.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_whitespaceTrack_exit2(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "   "})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_whitespacePackage_exit2(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	_, err := view.Run(rc, view.Input{Package: "   ", Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", api.calls())
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	api := &statusAPI{}
	rc, _ := newRC(t, api.serve(t))
	rc.Account = nil
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(api.calls()) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", api.calls())
	}
}
