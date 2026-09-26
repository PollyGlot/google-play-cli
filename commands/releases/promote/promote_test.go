// Package promote_test exercises `gplay releases promote` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// upload command's test harness so a single seam proves the auth +
// Edit lifecycle wiring for promote too.
package promote_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/releases/promote"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// promoteAPI configures the fake androidpublisher calls the promote
// orchestrator makes: edits.insert, tracks.get, tracks.update, edits.commit,
// edits.delete.
type promoteAPI struct {
	editID             string
	sourceTrackGetResp string
	trackUpdateRawResp string
	// trackUpdateStatus, when >= 400, makes the destination tracks.update PUT
	// fail with that status (carrying a Google error envelope) so tests can
	// exercise the track-not-found hint. 0 (the default) means a 200 success.
	trackUpdateStatus int
}

func newPromoteFake(a promoteAPI) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, a.editID), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/tracks/"):
			return http.StatusOK, a.sourceTrackGetResp, true
		case c.Method == http.MethodPut && strings.Contains(c.Path, "/tracks/"):
			if a.trackUpdateStatus >= 400 {
				return a.trackUpdateStatus, `{"error":{"code":404,"message":"Track not found."}}`, true
			}
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

// apiCalls lists the recorded API calls as "METHOD path".
func apiCalls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

// touched reports whether anything reached the transport, token exchange
// included.
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

// TestRenderTable_includesDefaultLanguage asserts that promote's
// table renderer prints the resolved DefaultLanguage when set, so the
// operator sees which locale a --release-notes override was attached
// to. Mirrors upload's behavior (upload.go:101-105): without this
// row, the operator has no visibility into the locale resolution.
func TestRenderTable_includesDefaultLanguage(t *testing.T) {
	p := promote.Payload{Result: &orchestrator.Result{
		VersionCode:     142,
		Track:           "beta",
		Status:          "completed",
		ReleaseName:     "142",
		DefaultLanguage: "fr-FR",
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "defaultLang:") {
		t.Errorf("table output = %q, want a defaultLang: row", out)
	}
	if !strings.Contains(out, "fr-FR") {
		t.Errorf("table output = %q, want the locale value 'fr-FR'", out)
	}
}

// TestRenderMarkdown_includesDefaultLanguageAndLocales asserts that
// the markdown renderer surfaces the same notes-resolution fields
// (DefaultLanguage, Locales) that the table renderer prints, so
// `--output markdown` and `--output table` don't diverge.
func TestRenderMarkdown_includesDefaultLanguageAndLocales(t *testing.T) {
	p := promote.Payload{Result: &orchestrator.Result{
		VersionCode:     142,
		Track:           "beta",
		Status:          "completed",
		ReleaseName:     "142",
		DefaultLanguage: "en-US",
		Locales:         []string{"en-US", "fr-FR"},
	}}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "**defaultLang**") {
		t.Errorf("markdown output = %q, want a **defaultLang** field", out)
	}
	if !strings.Contains(out, "en-US") {
		t.Errorf("markdown output = %q, want the locale value 'en-US'", out)
	}
	if !strings.Contains(out, "**locales**") {
		t.Errorf("markdown output = %q, want a **locales** field", out)
	}
	if !strings.Contains(out, "fr-FR") {
		t.Errorf("markdown output = %q, want fr-FR in the locales list", out)
	}
}

// TestRun_internalToBeta_happyPath asserts the full vertical slice from
// CLI input to wire: /token exchange precedes the androidpublisher
// edits.insert, then the canonical promote 4-call sequence runs.
func TestRun_internalToBeta_happyPath(t *testing.T) {
	api := promoteAPI{
		editID:             "edit-promote-cli",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	rt := newPromoteFake(api)
	rc, _ := newRC(t, rt)

	r, err := promote.Run(rc, promote.Input{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-promote-cli/tracks/internal",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-promote-cli/tracks/beta",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-promote-cli:commit",
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

	// ADR-0003: --output json must be API pass-through: the raw
	// tracks.update response body, not a gplay-shaped re-serialization.
	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(api.trackUpdateRawResp) {
		t.Errorf("JSON output = %s\nwant raw tracks.update payload = %s", got, api.trackUpdateRawResp)
	}
}

// TestRun_promoteToMissingClosedTrack_hintsTracksCreate asserts that a
// promote whose --to track has not been created yet (destination
// tracks.update 404) fails with a `gplay tracks create <name>` hint and
// preserves the underlying exit code (30, not rewritten by the hint). The
// source track read succeeds; only the destination is missing.
func TestRun_promoteToMissingClosedTrack_hintsTracksCreate(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-miss",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
		trackUpdateStatus:  http.StatusNotFound,
	})
	rc, _ := newRC(t, rt)

	_, err := promote.Run(rc, promote.Input{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "qa-team",
	})
	if err == nil {
		t.Fatal("Run returned nil error; want a track-not-found hint")
	}
	if !strings.Contains(err.Error(), "gplay tracks create qa-team") {
		t.Errorf("error %q is missing the `gplay tracks create qa-team` hint", err.Error())
	}
	if code := exit.For(err); code != 30 {
		t.Errorf("exit.For(err) = %d, want 30 (underlying *api.Error preserved); err=%v", code, err)
	}
}

// TestRun_missingFromOrTo_returnsExit2 asserts that --from and --to are
// required: the CLI must short-circuit with a usage error before any
// HTTP call when either is empty.
func TestRun_missingFromOrTo_returnsExit2(t *testing.T) {
	cases := []struct {
		name string
		in   promote.Input
	}{
		{"missing --from", promote.Input{Package: "com.example.app", ToTrack: "beta"}},
		{"missing --to", promote.Input{Package: "com.example.app", FromTrack: "internal"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newPromoteFake(promoteAPI{})
			rc, _ := newRC(t, rt)
			_, err := promote.Run(rc, tc.in)
			if err == nil {
				t.Fatal("Run returned nil error; want usage error")
			}
			var coder interface{ ExitCode() int }
			if !errors.As(err, &coder) {
				t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
			}
			if coder.ExitCode() != 2 {
				t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
			}
			if touched(rt) {
				t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
			}
		})
	}
}

// TestRun_mutuallyExclusiveStatusFlags_returnsExit2 asserts that
// passing more than one of --draft / --complete / --staged is a CLI
// misuse caught before any HTTP.
func TestRun_mutuallyExclusiveStatusFlags_returnsExit2(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	rc, _ := newRC(t, rt)

	_, err := promote.Run(rc, promote.Input{
		Package:           "com.example.app",
		FromTrack:         "internal",
		ToTrack:           "beta",
		Draft:             true,
		Complete:          true,
		StagedFractionSet: false,
	})
	if err == nil {
		t.Fatal("Run returned nil error; want usage error")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if touched(rt) {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", apiCalls(rt))
	}
}

// TestRun_happyPath_emitsConfirmationOnStderr asserts a committed promote
// prints a single ✓ line on stderr (DESIGN §8) carrying the versionCode and
// both tracks, alongside the stdout payload.
func TestRun_happyPath_emitsConfirmationOnStderr(t *testing.T) {
	rt := newPromoteFake(promoteAPI{
		editID:             "edit-promote-cli",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	})
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "internal", ToTrack: "beta"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("stderr missing ✓ confirmation line:\n%s", got)
	}
	for _, want := range []string{"142", "internal", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("✓ line %q missing %q", got, want)
		}
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	rt := newPromoteFake(promoteAPI{})
	rc, _ := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := promote.Run(rc, promote.Input{Package: "com.example.app", FromTrack: "internal", ToTrack: "beta", DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}
