package list_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func newListFake(editID string, getCode int, getResp string) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			return getCode, getResp, true
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

func assertReadOnly(t *testing.T, f *testkit.Fake) {
	t.Helper()
	for _, c := range f.Calls() {
		if c.Method == http.MethodPut || c.Method == http.MethodPatch || strings.HasSuffix(c.Path, ":commit") {
			t.Errorf("read-only list must not write/commit: %s %s", c.Method, c.Path)
		}
	}
}

func touched(f *testkit.Fake) bool { return len(f.Calls()) != 0 || f.TokenExchanges() != 0 }

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

func TestRun_listsAllReleases_happyPath(t *testing.T) {
	raw := `{"track":"production","releases":[` +
		`{"name":"150-draft","status":"draft","versionCodes":["150"]},` +
		`{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.1},` +
		`{"name":"141","status":"halted","versionCodes":["141"],"userFraction":0.5},` +
		`{"name":"140","status":"completed","versionCodes":["140"],"userFraction":1.0,"releaseNotes":[{"language":"en-US","text":"x"},{"language":"fr-FR","text":"y"}]}` +
		`]}`
	rt := newListFake("edit-list", 0, raw)
	rc, _ := newRC(t, rt)

	r, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if rt.TokenExchanges() == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", apiCalls(rt))
	}
	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-list/tracks/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-list",
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
	assertReadOnly(t, rt)

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(raw) {
		t.Errorf("JSON output = %s\nwant raw tracks.get payload = %s", got, raw)
	}
}

func TestRun_unknownTrack_exit30WithHint(t *testing.T) {
	rt := newListFake("edit-404", 404, `{"error":{"code":404,"message":"Track not found."}}`)
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "bogus"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "tracks list") {
		t.Errorf("error %q, want a hint mentioning `gplay tracks list`", err.Error())
	}
	sawDelete := false
	for _, c := range apiCalls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-404") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after 404; calls = %v", apiCalls(rt))
	}
	assertReadOnly(t, rt)
}

func TestRun_missingTrack_exit2(t *testing.T) {
	rt := newListFake("", 0, "")
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRun_missingPackage_exit2(t *testing.T) {
	rt := newListFake("", 0, "")
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	rt := newListFake("", 0, "")
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", apiCalls(rt))
	}
}

func TestRun_unknownColumn_exit2(t *testing.T) {
	rt := newListFake("", 0, "")
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production", Columns: "name,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

func TestRenderTable_defaultColumns_showsEveryRelease(t *testing.T) {
	defCols, _ := list.ResolveColumns("")
	p := list.Payload{
		Track: "production",
		Releases: []tracks.Release{
			{Name: "150-draft", Status: "draft", VersionCodes: []string{"150"}},
			{Name: "142", Status: "inProgress", VersionCodes: []string{"142"}, UserFraction: 0.1},
			{Name: "141", Status: "halted", VersionCodes: []string{"141"}, UserFraction: 0.5},
		},
		Columns: defCols,
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"draft", "inProgress", "halted", "150-draft", "142", "141", "0.1", "0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTable_emptyReleases_printsFriendlyMessage(t *testing.T) {
	defCols, _ := list.ResolveColumns("")
	p := list.Payload{Track: "alpha", Releases: nil, Columns: defCols}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(buf.String(), "alpha") {
		t.Errorf("empty table output = %q, want it to name the track", buf.String())
	}
}

func TestRenderMarkdown_isMarkdownTable_respectsColumnsOverride(t *testing.T) {
	cols, _ := list.ResolveColumns("name,status")
	p := list.Payload{
		Track: "production",
		Releases: []tracks.Release{
			{Name: "142", Status: "inProgress", VersionCodes: []string{"142"}, UserFraction: 0.1},
		},
		Columns: cols,
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "142") || !strings.Contains(out, "inProgress") {
		t.Errorf("markdown output = %q, want name+status cells", out)
	}
	if strings.Contains(strings.ToLower(out), "version") {
		t.Errorf("markdown output = %q, want no versionCodes column under --columns name,status", out)
	}
}
