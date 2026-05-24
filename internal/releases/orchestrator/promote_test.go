// Package orchestrator_test exercises the promote orchestration via a
// RoundTripper-mocked Google Play Developer API. The tracer bullet here
// is the canonical four-call sequence on a non-production destination:
// edits.insert → tracks.get(source) → tracks.update(dest) → edits.commit.
package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/releases/orchestrator"
)

// promoteRT routes calls to the androidpublisher endpoints involved in a
// promote: edits.insert, tracks.get (source), tracks.update (dest),
// edits.commit, and the cleanup edits.delete. Each endpoint has a
// default happy-path handler; tests override any specific handler to
// inject a failure or to assert on the request body.
type promoteRT struct {
	t                  *testing.T
	editID             string
	sourceTrackGetResp string // body returned for GET /tracks/<from>
	trackUpdateRawResp string // body returned for PUT /tracks/<to>
	defaultLanguage    string // body returned for GET /details (empty → en-US)

	insertHandler  func(req *http.Request) (*http.Response, error)
	getHandler     func(req *http.Request) (*http.Response, error)
	detailsHandler func(req *http.Request) (*http.Response, error)
	updateHandler  func(req *http.Request) (*http.Response, error)
	commitHandler  func(req *http.Request) (*http.Response, error)
	deleteHandler  func(req *http.Request) (*http.Response, error)

	calls          []string
	trackUpdateReq []byte
}

func (p *promoteRT) RoundTrip(req *http.Request) (*http.Response, error) {
	p.calls = append(p.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		if p.insertHandler != nil {
			return p.insertHandler(req)
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, p.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		if p.deleteHandler != nil {
			return p.deleteHandler(req)
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/details"):
		if p.detailsHandler != nil {
			return p.detailsHandler(req)
		}
		lang := p.defaultLanguage
		if lang == "" {
			lang = "en-US"
		}
		return jsonResp(200, fmt.Sprintf(`{"defaultLanguage":%q,"contactEmail":"x@example.com"}`, lang)), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tracks/"):
		if p.getHandler != nil {
			return p.getHandler(req)
		}
		return jsonResp(200, p.sourceTrackGetResp), nil
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/tracks/"):
		if p.updateHandler != nil {
			return p.updateHandler(req)
		}
		body, _ := io.ReadAll(req.Body)
		p.trackUpdateReq = body
		resp := p.trackUpdateRawResp
		if resp == "" {
			resp = `{}`
		}
		return jsonResp(200, resp), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		if p.commitHandler != nil {
			return p.commitHandler(req)
		}
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"0"}`, p.editID)), nil
	}
	p.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

// TestPromote_internalToBeta_happyPath_copiesVersionCode is the tracer
// bullet for orchestrator.Promote: a complete vertical slice through
// the promote flow on a non-production destination. Asserts the
// canonical four-call sequence (edits.insert → tracks.get(source) →
// tracks.update(dest) → edits.commit) and that the dest update carries
// the same versionCode as the source plus status=completed,
// userFraction=1.0 (safe-default for non-production targets).
func TestPromote_internalToBeta_happyPath_copiesVersionCode(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-promote",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// Canonical four-call sequence.
	wantPaths := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-promote/tracks/internal",
		"PUT /androidpublisher/v3/applications/com.example.app/edits/edit-promote/tracks/beta",
		"POST /androidpublisher/v3/applications/com.example.app/edits/edit-promote:commit",
	}
	if len(rt.calls) != len(wantPaths) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantPaths))
	}
	for i, want := range wantPaths {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// Track update payload carries versionCode 142, status=completed, userFraction=1.0.
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"versionCodes":["142"]`) {
		t.Errorf("tracks.update body = %s, want versionCodes=[\"142\"]", body)
	}
	if !strings.Contains(body, `"status":"completed"`) {
		t.Errorf("tracks.update body = %s, want status=completed", body)
	}
	if !strings.Contains(body, `"userFraction":1`) {
		t.Errorf("tracks.update body = %s, want userFraction=1.0", body)
	}

	// Result carries versionCode parsed from the source release.
	if result.VersionCode != 142 {
		t.Errorf("VersionCode = %d, want 142", result.VersionCode)
	}
	if result.Track != "beta" {
		t.Errorf("Track = %q, want beta", result.Track)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

// TestPromote_internalToProduction_safeDefault_sendsDraftStatus asserts
// the ADR-0002 safe-default rule applied to the destination of a
// promote: target production with Status=Unspecified produces a draft
// release (no userFraction field). The source release's completed
// status does NOT leak through.
func TestPromote_internalToProduction_safeDefault_sendsDraftStatus(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-promote-prod",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"200","status":"completed","versionCodes":["200"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"200","status":"draft","versionCodes":["200"]}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "production",
		// Status: StatusUnspecified (default zero value) → safe-default applies.
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"draft"`) {
		t.Errorf("production safe-default: body = %s, want status=draft", body)
	}
	if strings.Contains(body, `"userFraction"`) {
		t.Errorf("production safe-default: body = %s, want userFraction omitted for draft", body)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
	if result.UserFraction != 0 {
		t.Errorf("result.UserFraction = %v, want 0", result.UserFraction)
	}
}

// TestPromote_toProduction_explicitStaged_sendsInProgressWithFraction
// asserts that --staged 0.05 (Status=InProgress + UserFraction=0.05)
// on a production target ships inProgress at 5% — overriding the
// ADR-0002 safe-default for that destination. Production publish that
// reaches real users requires Confirm=true (mirrors upload).
func TestPromote_toProduction_explicitStaged_sendsInProgressWithFraction(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-promote-staged",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"301","status":"completed","versionCodes":["301"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"301","status":"inProgress","versionCodes":["301"],"userFraction":0.05}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:      "com.example.app",
		FromTrack:    "beta",
		ToTrack:      "production",
		Status:       orchestrator.StatusInProgress,
		UserFraction: 0.05,
		Confirm:      true,
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"status":"inProgress"`) {
		t.Errorf("body = %s, want status=inProgress", body)
	}
	if !strings.Contains(body, `"userFraction":0.05`) {
		t.Errorf("body = %s, want userFraction=0.05", body)
	}
	if result.Status != "inProgress" || result.UserFraction != 0.05 {
		t.Errorf("result = (%q, %v), want (inProgress, 0.05)", result.Status, result.UserFraction)
	}
}

// TestPromote_ambiguousSource_noDisambiguator_returnsExit60Error asserts
// the AC4 rule from issue #45: when the source track carries multiple
// coexisting releases (e.g. inProgress + halted) and the caller did
// not pass --version-code or --release-name, Promote must refuse with
// an *AmbiguousReleaseError (ExitCode=60) listing the candidate
// versionCodes — and MUST NOT write to the destination.
func TestPromote_ambiguousSource_noDisambiguator_returnsExit60Error(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-ambiguous",
		// Two coexisting releases: halted at fraction 0.10 and inProgress at 0.50.
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update was called despite ambiguous source: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "production",
		ToTrack:   "internal",
	})
	if err == nil {
		t.Fatal("Promote(ambiguous source without disambiguator): want error, got nil")
	}

	var ambig *orchestrator.AmbiguousReleaseError
	if !errors.As(err, &ambig) {
		t.Fatalf("err = %v (%T), want *orchestrator.AmbiguousReleaseError", err, err)
	}
	if ambig.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", ambig.ExitCode())
	}
	// The error must list the candidate versionCodes so the operator
	// can pick one. Both 500 and 501 must appear in the message.
	msg := ambig.Error()
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "501") {
		t.Errorf("AmbiguousReleaseError.Error() = %q, want both candidate versionCodes (500, 501)", msg)
	}

	// The error path must run BEFORE any tracks.update on the
	// destination. (No PUT in calls.)
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "PUT ") {
			t.Errorf("destination tracks.update should not be called on ambiguous source; calls = %v", rt.calls)
		}
	}
}

// TestPromote_ambiguousSource_versionCodeDisambiguates asserts that
// --version-code picks the matching release out of multiple
// coexisting ones, and that the non-matching ones are ignored.
func TestPromote_ambiguousSource_versionCodeDisambiguates(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-disambig",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"500","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"501","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[{"name":"501","status":"completed","versionCodes":["501"],"userFraction":1.0}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 501,
	})
	if err != nil {
		t.Fatalf("Promote with --version-code 501: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("VersionCode = %d, want 501", result.VersionCode)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, `"versionCodes":["501"]`) {
		t.Errorf("body = %s, want versionCodes=[\"501\"] (the picked release)", body)
	}
	if strings.Contains(body, `"versionCodes":["500"]`) {
		t.Errorf("body = %s, should NOT include the non-picked release 500", body)
	}
}

// TestPromote_ambiguousSource_releaseNameDisambiguates asserts the
// same flow via --release-name (an explicit Name match on the source
// Release).
func TestPromote_ambiguousSource_releaseNameDisambiguates(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-disambig-name",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"v2.0-rc1","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"v2.0-rc2","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		ReleaseName: "v2.0-rc2",
	})
	if err != nil {
		t.Fatalf("Promote with --release-name v2.0-rc2: %v", err)
	}
	if result.VersionCode != 501 {
		t.Errorf("VersionCode = %d, want 501 (the rc2 release)", result.VersionCode)
	}
}

// TestPromote_versionCodeNotFound_returnsExit60Error asserts that when
// --version-code is supplied but does not match any release on the
// source track, Promote refuses with an error carrying ExitCode=60
// (state conflict) rather than silently falling back to "latest".
func TestPromote_versionCodeNotFound_returnsExit60Error(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-not-found",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"500","status":"completed","versionCodes":["500"],"userFraction":1.0}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update was called despite missing --version-code match: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 999, // not on the source
	})
	if err == nil {
		t.Fatal("Promote(unknown --version-code): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", coder.ExitCode())
	}
}

// TestPromote_ambiguousAfterReleaseName_hintSaysFilterAlreadyApplied
// asserts that when the user passed --release-name and the filter
// still matched multiple releases (Play allows duplicate names), the
// AmbiguousReleaseError message tells them to TIGHTEN the filter
// rather than the unhelpful default "pass --release-name to pick
// one" — which they already did.
func TestPromote_ambiguousAfterReleaseName_hintSaysFilterAlreadyApplied(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-ambig-after-filter",
		sourceTrackGetResp: `{"track":"production","releases":[` +
			`{"name":"v2.0","status":"halted","versionCodes":["500"],"userFraction":0.10},` +
			`{"name":"v2.0","status":"inProgress","versionCodes":["501"],"userFraction":0.50}` +
			`]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		ReleaseName: "v2.0",
	})
	if err == nil {
		t.Fatal("Promote(ambiguous after filter): want error, got nil")
	}
	var ambig *orchestrator.AmbiguousReleaseError
	if !errors.As(err, &ambig) {
		t.Fatalf("err = %v (%T), want *AmbiguousReleaseError", err, err)
	}
	if !ambig.FilterApplied {
		t.Errorf("FilterApplied = false, want true (caller passed --release-name)")
	}
	msg := ambig.Error()
	// Hint should not repeat "pass --release-name" when the caller
	// already did. "tighten" is the verb we expect for this case.
	if !strings.Contains(msg, "tighten") && !strings.Contains(msg, "combine") {
		t.Errorf("err = %q, want hint to mention tightening/combining the existing filter, not repeating it", msg)
	}
}

// TestPromote_noMatchingRelease_listsBothCriteria asserts that when
// both --version-code AND --release-name are supplied, the
// NoMatchingReleaseError message names both — not just one. Without
// this, an operator looking at the error sees only the version-code
// constraint, misses that --release-name also constrained the match,
// and burns time debugging the wrong filter.
func TestPromote_noMatchingRelease_listsBothCriteria(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-both-filters",
		sourceTrackGetResp: `{"track":"production","releases":[{"name":"v1.9-hotfix","status":"completed","versionCodes":["142"],"userFraction":1.0}]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:     "com.example.app",
		FromTrack:   "production",
		ToTrack:     "beta",
		VersionCode: 142,
		ReleaseName: "v2.0",
	})
	if err == nil {
		t.Fatal("Promote(both filters, neither matches together): want error, got nil")
	}
	var nme *orchestrator.NoMatchingReleaseError
	if !errors.As(err, &nme) {
		t.Fatalf("err = %v (%T), want *NoMatchingReleaseError", err, err)
	}
	msg := nme.Error()
	if !strings.Contains(msg, "--version-code=142") {
		t.Errorf("err = %q, want --version-code=142 mentioned", msg)
	}
	if !strings.Contains(msg, "--release-name=v2.0") {
		t.Errorf("err = %q, want --release-name=v2.0 mentioned", msg)
	}
}

// TestPromote_releaseNotesCarryOver_verbatim asserts that when neither
// --release-notes nor --release-notes-dir is passed, the source
// release's releaseNotes payload is forwarded verbatim to the
// destination (the AC5 default behavior from issue #45). No call to
// edits.details.get — the source already carries locale-keyed text.
func TestPromote_releaseNotesCarryOver_verbatim(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-notes-carry",
		// Source release carries 2 locales of notes.
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"600","status":"completed","versionCodes":["600"],"userFraction":1.0,` +
			`"releaseNotes":[` +
			`{"language":"en-US","text":"English notes from source"},` +
			`{"language":"fr-FR","text":"Notes en français"}` +
			`]}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
		// No --release-notes / --release-notes-dir → carry-over from source.
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(rt.trackUpdateReq)
	for _, want := range []string{
		`"language":"en-US"`,
		`"language":"fr-FR"`,
		"English notes from source",
		"Notes en français",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want substring %q (carry-over)", body, want)
		}
	}

	// No edits.details.get round-trip — the source already gave us
	// locale-keyed notes, no DefaultLanguage resolution needed.
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			t.Errorf("edits.details.get called despite verbatim carry-over: %v", rt.calls)
		}
	}

	// Result advertises the carried-over locales.
	if len(result.Locales) != 2 {
		t.Errorf("result.Locales = %v, want 2 entries (en-US, fr-FR)", result.Locales)
	}
}

// TestPromote_releaseNotesCarryOver_noSourceNotes asserts the
// degenerate case: source release has no releaseNotes, dest also
// has none. No spurious empty array, no details.get call.
func TestPromote_releaseNotesCarryOver_noSourceNotes(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-no-notes",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"700","status":"completed","versionCodes":["700"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(rt.trackUpdateReq)
	if strings.Contains(body, `"releaseNotes"`) {
		t.Errorf("body = %s, should NOT include empty releaseNotes when source has none", body)
	}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			t.Errorf("edits.details.get called despite no notes: %v", rt.calls)
		}
	}
}

// TestPromote_releaseNotesFlag_overridesCarryOver asserts that
// --release-notes "text" drops the source's notes and writes the new
// text against the app's DefaultLanguage (requires an edits.details.get
// round-trip). Mirrors upload's --release-notes flow.
func TestPromote_releaseNotesFlag_overridesCarryOver(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "edit-notes-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"800","status":"completed","versionCodes":["800"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"OLD source notes"}]` +
			`}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:      "com.example.app",
		FromTrack:    "internal",
		ToTrack:      "beta",
		ReleaseNotes: "Fresh notes for the promotion",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, "Fresh notes for the promotion") {
		t.Errorf("body = %s, want the --release-notes text", body)
	}
	if strings.Contains(body, "OLD source notes") {
		t.Errorf("body = %s, must NOT carry over source notes when --release-notes is set", body)
	}
	if !strings.Contains(body, `"language":"en-US"`) {
		t.Errorf("body = %s, want notes attached to the app DefaultLanguage (en-US)", body)
	}

	// details.get must be called to resolve DefaultLanguage for the
	// single-text --release-notes path.
	sawDetails := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "GET ") && strings.HasSuffix(c, "/details") {
			sawDetails = true
			break
		}
	}
	if !sawDetails {
		t.Errorf("--release-notes set but no details.get call; calls = %v", rt.calls)
	}
	if result.DefaultLanguage != "en-US" {
		t.Errorf("result.DefaultLanguage = %q, want en-US", result.DefaultLanguage)
	}
}

// TestPromote_releaseNotesDir_emptyOrTypod_refusesRatherThanDropSource
// asserts that an override directory containing no .txt files refuses
// the promote rather than silently dropping the source's notes. The
// promote DEFAULT is verbatim carry-over; if the user passes a dir
// that yields zero notes, they have asked to override with nothing —
// which would erase the source's release notes on the destination.
// Fail-loud beats silent data loss.
func TestPromote_releaseNotesDir_emptyOrTypod_refusesRatherThanDropSource(t *testing.T) {
	emptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyDir, "README.md"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &promoteRT{
		t:      t,
		editID: "edit-empty-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"800","status":"completed","versionCodes":["800"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"source notes"}]` +
			`}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update was called despite empty override dropping source notes: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: emptyDir,
	})
	if err == nil {
		t.Fatal("Promote(empty override dir + notes on source): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if !strings.Contains(err.Error(), "en-US") {
		t.Errorf("err = %q, want it to name the dropped locale 'en-US'", err.Error())
	}
}

// TestPromote_releaseNotesDir_partialOverride_refusesRatherThanDropOtherLocales
// asserts that an override dir with FEWER locales than the source
// refuses rather than silently dropping the locales the override
// does not cover. Without this guard, `--release-notes-dir ./fix`
// containing only fr-FR.txt against a source with en-US + de-DE + fr-FR
// would ship a release with only fr-FR — wiping the other two
// locales on the destination.
func TestPromote_releaseNotesDir_partialOverride_refusesRatherThanDropOtherLocales(t *testing.T) {
	partialDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(partialDir, "fr-FR.txt"), []byte("notes en français"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &promoteRT{
		t:      t,
		editID: "edit-partial-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"801","status":"completed","versionCodes":["801"],"userFraction":1.0,` +
			`"releaseNotes":[` +
			`{"language":"en-US","text":"English notes"},` +
			`{"language":"de-DE","text":"German notes"},` +
			`{"language":"fr-FR","text":"old French notes"}` +
			`]}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update was called despite partial override dropping source locales: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: partialDir,
	})
	if err == nil {
		t.Fatal("Promote(partial override + multi-locale source): want error, got nil")
	}
	msg := err.Error()
	// Both dropped locales should appear in the error.
	if !strings.Contains(msg, "en-US") || !strings.Contains(msg, "de-DE") {
		t.Errorf("err = %q, want it to name dropped locales en-US and de-DE", msg)
	}
}

// TestPromote_releaseNotesDir_coversAllSourceLocales_succeeds asserts
// the inverse: when the override dir DOES cover every source locale
// (here, en-US is the only source locale and the dir has en-US.txt),
// the promote succeeds — no spurious refusal.
func TestPromote_releaseNotesDir_coversAllSourceLocales_succeeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), []byte("Fresh English notes"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &promoteRT{
		t:      t,
		editID: "edit-full-override",
		sourceTrackGetResp: `{"track":"internal","releases":[{` +
			`"name":"802","status":"completed","versionCodes":["802"],"userFraction":1.0,` +
			`"releaseNotes":[{"language":"en-US","text":"OLD"}]` +
			`}]}`,
		trackUpdateRawResp: `{"track":"beta","releases":[]}`,
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:         "com.example.app",
		FromTrack:       "internal",
		ToTrack:         "beta",
		ReleaseNotesDir: dir,
	})
	if err != nil {
		t.Fatalf("Promote(override covers all source locales): %v", err)
	}
	body := string(rt.trackUpdateReq)
	if !strings.Contains(body, "Fresh English notes") {
		t.Errorf("body = %s, want the override text", body)
	}
}

// TestPromote_productionComplete_withoutConfirm_returnsExit2 asserts
// the ADR-0002 confirm guard inheritance: a production promote that
// would reach real users (Completed) without Confirm=true must refuse
// before any HTTP. Mirrors upload's same guard so users learn the rule
// once.
func TestPromote_productionComplete_withoutConfirm_returnsExit2(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("insert was called despite missing --confirm: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		Status:    orchestrator.StatusCompleted,
		Confirm:   false,
	})
	if err == nil {
		t.Fatal("Promote(production+Completed without Confirm): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before --confirm guard, saw: %v", rt.calls)
	}
}

// TestPromote_productionDraft_withoutConfirm_works asserts the inverse:
// a draft promote to production (the safe-default path) does NOT need
// --confirm, since draft does not affect real users.
func TestPromote_productionDraft_withoutConfirm_works(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-prod-draft",
		sourceTrackGetResp: `{"track":"beta","releases":[{"name":"900","status":"completed","versionCodes":["900"],"userFraction":1.0}]}`,
		trackUpdateRawResp: `{"track":"production","releases":[{"name":"900","status":"draft","versionCodes":["900"]}]}`,
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		// Status unspecified → safe-default = draft → no confirm needed.
	})
	if err != nil {
		t.Fatalf("Promote(production safe-default): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

// TestPromote_trackUpdateFail_triggersEditDelete asserts the
// auto-discard safety net: when tracks.update on the destination
// returns 5xx, the orphan Edit MUST be cleaned up via edits.delete
// before the error propagates (inherited from edits.WithEdit). A
// leaked Edit blocks the user's next publish for up to 24h.
func TestPromote_trackUpdateFail_triggersEditDelete(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-promote-fail",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"950","status":"completed","versionCodes":["950"],"userFraction":1.0}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(503, `{"error":{"code":503,"message":"service unavailable"}}`), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err == nil {
		t.Fatal("Promote: want error after track update failure, got nil")
	}

	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-promote-fail") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after track update failure; calls = %v", rt.calls)
	}
}

// TestPromote_validateOpts_libraryCallers asserts that orchestrator.Promote
// rejects malformed PromoteOpts before opening an Edit — symmetric with
// Upload's validateOpts. The CLI layer ALSO catches these cases, but
// the orchestrator is documented as a library entry point and must
// enforce its own contract so library / future-MCP callers do not
// burn an Edit (a 24h resource) on inputs the orchestrator could have
// refused statically.
func TestPromote_validateOpts_libraryCallers(t *testing.T) {
	cases := []struct {
		name string
		opts orchestrator.PromoteOpts
	}{
		{
			name: "missing FromTrack",
			opts: orchestrator.PromoteOpts{Package: "com.example.app", ToTrack: "beta"},
		},
		{
			name: "missing ToTrack",
			opts: orchestrator.PromoteOpts{Package: "com.example.app", FromTrack: "internal"},
		},
		{
			name: "ReleaseNotes and ReleaseNotesDir both set",
			opts: orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
				ReleaseNotes: "x", ReleaseNotesDir: "/tmp/notes",
			},
		},
		{
			name: "InProgress with UserFraction 0",
			opts: orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
				Status: orchestrator.StatusInProgress, UserFraction: 0,
			},
		},
		{
			name: "InProgress with UserFraction > 1",
			opts: orchestrator.PromoteOpts{
				Package: "com.example.app", FromTrack: "internal", ToTrack: "beta",
				Status: orchestrator.StatusInProgress, UserFraction: 1.5,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &promoteRT{
				t:      t,
				editID: "should-not-open",
				insertHandler: func(req *http.Request) (*http.Response, error) {
					t.Errorf("insert was called despite invalid opts: %s", req.URL.Path)
					return jsonResp(500, ""), nil
				},
			}
			hc := &http.Client{Transport: rt}

			_, err := orchestrator.Promote(context.Background(), hc, tc.opts)
			if err == nil {
				t.Fatal("Promote(invalid opts): want error, got nil")
			}
			var coder interface{ ExitCode() int }
			if !errors.As(err, &coder) {
				t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
			}
			if coder.ExitCode() != 2 {
				t.Errorf("ExitCode() = %d, want 2 (InvalidOptsError)", coder.ExitCode())
			}
			if len(rt.calls) != 0 {
				t.Errorf("expected zero HTTP calls before validation error, saw: %v", rt.calls)
			}
		})
	}
}

// TestPromote_dryRun_makesNoHTTPCalls asserts that PromoteOpts.DryRun
// completes successfully with zero HTTP calls and returns a preview
// Result describing what would be sent. The coding guideline requires
// every releases write op to support --dry-run; the orchestrator
// honors it BEFORE the confirm guard so operators can preview a
// production publish without committing to --confirm.
func TestPromote_dryRun_makesNoHTTPCalls(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("HTTP call in dry-run mode: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run): %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("dry-run made HTTP calls: %v", rt.calls)
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed (non-production safe-default preview)", result.Status)
	}
	if result.ReleaseName != "(dry-run)" {
		t.Errorf("result.ReleaseName = %q, want (dry-run)", result.ReleaseName)
	}
	if result.Track != "beta" {
		t.Errorf("result.Track = %q, want beta", result.Track)
	}
}

// TestPromote_dryRun_productionSafeDefault asserts dry-run also applies
// the ADR-0002 safe-default — production target with no status flag
// previews as draft, with no HTTP and without needing --confirm.
func TestPromote_dryRun_productionSafeDefault(t *testing.T) {
	rt := &promoteRT{t: t}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run production): %v", err)
	}
	if result.Status != "draft" {
		t.Errorf("result.Status = %q, want draft", result.Status)
	}
}

// TestPromote_dryRun_productionCompleteNoConfirm_previewsWithoutGate
// asserts dry-run runs BEFORE the confirm guard so a user can preview
// a production publish (e.g. --complete) without having to satisfy
// --confirm. The live path still requires --confirm for the same
// inputs (covered by other tests).
func TestPromote_dryRun_productionCompleteNoConfirm_previewsWithoutGate(t *testing.T) {
	rt := &promoteRT{t: t}
	hc := &http.Client{Transport: rt}

	result, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "beta",
		ToTrack:   "production",
		Status:    orchestrator.StatusCompleted,
		Confirm:   false,
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Promote(dry-run production complete, no confirm): %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("result.Status = %q, want completed", result.Status)
	}
	if result.UserFraction != 1.0 {
		t.Errorf("result.UserFraction = %v, want 1.0", result.UserFraction)
	}
}

// TestPromote_dryRun_invalidOptsStillCaught asserts that orchestrator-
// level validation (validatePromoteOpts) runs even in dry-run mode —
// so a caller previewing with bad inputs still gets the right error
// instead of a misleading "looks fine, would have shipped" preview.
func TestPromote_dryRun_invalidOptsStillCaught(t *testing.T) {
	rt := &promoteRT{t: t}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package: "com.example.app",
		// FromTrack missing
		ToTrack: "beta",
		DryRun:  true,
	})
	if err == nil {
		t.Fatal("Promote(dry-run with missing FromTrack): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
}

// TestPromote_unknownStatusValue_returnsExit2Error asserts that a
// Status value outside the four declared constants is rejected with
// *InvalidOptsError (exit 2) before any HTTP. Without this guard,
// statusPayload's switch falls through to `return "", 0` and ships a
// release with status=” that the API rejects opaquely — and a future
// new Status constant added to the enum but not wired in statusPayload
// would silently break.
func TestPromote_unknownStatusValue_returnsExit2Error(t *testing.T) {
	rt := &promoteRT{
		t:      t,
		editID: "should-not-open",
		insertHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("insert was called despite invalid Status: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
		Status:    orchestrator.Status(99), // not a declared constant
	})
	if err == nil {
		t.Fatal("Promote(unknown Status): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before Status validation, saw: %v", rt.calls)
	}
}

// TestPromote_sourceReleaseEmptyVersionCodes_returnsExit60Error asserts
// that a source release with no versionCodes (a legitimate draft
// placeholder on the Play API) does NOT panic on the
// `source.VersionCodes[0]` index — instead the orchestrator returns a
// typed error carrying ExitCode=60 (state conflict). Without this
// guard, the index-out-of-range panic also unwinds past WithEdit's
// auto-discard path and leaks the open Edit for ~24h.
func TestPromote_sourceReleaseEmptyVersionCodes_returnsExit60Error(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-no-codes",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"v2.0-draft","status":"draft"}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("tracks.update was called despite empty source versionCodes: %s", req.URL.Path)
			return jsonResp(500, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:   "com.example.app",
		FromTrack: "internal",
		ToTrack:   "beta",
	})
	if err == nil {
		t.Fatal("Promote(source release with no versionCodes): want typed error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 60 {
		t.Errorf("ExitCode() = %d, want 60", coder.ExitCode())
	}

	// Auto-discard must have run (no panic = WithEdit handleFailure
	// ran the cleanup branch).
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-no-codes") {
			sawDelete = true
			break
		}
	}
	if !sawDelete {
		t.Errorf("auto-discard not triggered after empty-versionCodes error; calls = %v", rt.calls)
	}
}

// TestPromote_trackUpdateFail_keepOnFailure_doesNotDelete asserts the
// --keep-edit-on-failure opt-out: a tracks.update failure must NOT
// trigger edits.delete when KeepEditOnFailure=true, and the returned
// error must be a *edits.DanglingEditError carrying the Edit ID so the
// operator can recover.
func TestPromote_trackUpdateFail_keepOnFailure_doesNotDelete(t *testing.T) {
	rt := &promoteRT{
		t:                  t,
		editID:             "edit-promote-keep",
		sourceTrackGetResp: `{"track":"internal","releases":[{"name":"951","status":"completed","versionCodes":["951"],"userFraction":1.0}]}`,
		updateHandler: func(req *http.Request) (*http.Response, error) {
			return jsonResp(503, `{"error":{"code":503,"message":"service unavailable"}}`), nil
		},
		deleteHandler: func(req *http.Request) (*http.Response, error) {
			t.Errorf("DELETE was called despite KeepEditOnFailure=true: %s", req.URL.Path)
			return jsonResp(204, ""), nil
		},
	}
	hc := &http.Client{Transport: rt}

	_, err := orchestrator.Promote(context.Background(), hc, orchestrator.PromoteOpts{
		Package:           "com.example.app",
		FromTrack:         "internal",
		ToTrack:           "beta",
		KeepEditOnFailure: true,
	})
	if err == nil {
		t.Fatal("Promote: want error after track update failure, got nil")
	}
	var dangling *edits.DanglingEditError
	if !errors.As(err, &dangling) {
		t.Fatalf("err = %v (%T), want *edits.DanglingEditError", err, err)
	}
	if dangling.EditID != "edit-promote-keep" {
		t.Errorf("DanglingEditError.EditID = %q, want edit-promote-keep", dangling.EditID)
	}
}
