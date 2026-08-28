// Package auditcmd_test drives `gplay apps audit` at the kernel level: a
// RunContext built by hand and a RoundTripper injected via the
// oauth2.HTTPClient context key, so the whole sweep runs offline.
//
// The transport is the read-only guard PRD #449 asks for: it serves the
// Reporting discovery call and the per-app reads, and FAILS the test on any
// mutating request. The one exception is the Edit envelope every gplay read
// rides (POST .../edits then DELETE .../edits/{id}); the transport asserts the
// discard actually happens, so "read-only" here means "no app state is ever
// written and no Edit is ever committed", the same contract `tracks list`
// enforces.
package auditcmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/internal/audit"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// appFixture is one app's canned API state.
type appFixture struct {
	tracksBody   string
	listingsBody string
	// status, when non-zero, makes the app's tracks.list fail with that HTTP
	// status: the partial-failure path.
	status int
}

// auditRT routes the whole sweep offline. Any request that is not the token
// exchange, the Reporting search, a GET read, or the Edit open/discard fails
// the test: an audit that PUTs, PATCHes or commits is a contract breach, not a
// wrong answer.
type auditRT struct {
	t          *testing.T
	searchBody string
	apps       map[string]appFixture

	mu       sync.Mutex
	calls    []string
	openEdit map[string]bool
	discards int
}

func (r *auditRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"a","token_type":"Bearer","expires_in":3600}`), nil
	}
	path := req.URL.Path
	r.calls = append(r.calls, req.Method+" "+path)

	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/apps:search"):
		return jsonResp(200, r.searchBody), nil

	case req.Method == http.MethodPost && strings.HasSuffix(path, "/edits"):
		pkg := pkgOf(path)
		if r.openEdit == nil {
			r.openEdit = map[string]bool{}
		}
		r.openEdit[pkg] = true
		return jsonResp(200, fmt.Sprintf(`{"id":"edit-%s","expiryTimeSeconds":"1700000000"}`, pkg)), nil

	case req.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
		r.discards++
		delete(r.openEdit, pkgOf(path))
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil

	case req.Method == http.MethodGet && strings.HasSuffix(path, "/tracks"):
		f := r.apps[pkgOf(path)]
		if f.status != 0 {
			return jsonResp(f.status, `{"error":{"message":"nope"}}`), nil
		}
		return jsonResp(200, f.tracksBody), nil

	case req.Method == http.MethodGet && strings.HasSuffix(path, "/listings"):
		return jsonResp(200, r.apps[pkgOf(path)].listingsBody), nil
	}

	r.t.Fatalf("audit issued a non-read request (it must never mutate): %s %s", req.Method, req.URL)
	return nil, nil
}

// pkgOf pulls the package out of an androidpublisher applications path.
func pkgOf(path string) string {
	const marker = "/applications/"
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, err := json.Marshal(map[string]any{
		"type":         "service_account",
		"project_id":   "test-proj",
		"private_key":  string(pemBytes),
		"client_email": "playci@test-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout
}

const (
	// driftedTracks: a lingering draft on internal, and a production release
	// with no release notes.
	driftedTracks = `{"tracks":[
		{"track":"internal","releases":[{"name":"3.0.0","status":"draft","versionCodes":["30"]}]},
		{"track":"production","releases":[{"name":"2.0.0","status":"completed","versionCodes":["20"]}]}
	]}`
	// cleanTracks: production shipped with notes, nothing in draft.
	cleanTracks = `{"tracks":[
		{"track":"production","releases":[{"name":"2.0.0","status":"completed","versionCodes":["20"],
			"releaseNotes":[{"language":"en-US","text":"Bug fixes"},{"language":"fr-FR","text":"Corrections"}]}]}
	]}`
	twoLocales = `{"listings":[{"language":"en-US","title":"A"},{"language":"fr-FR","title":"A"}]}`
	oneLocale  = `{"listings":[{"language":"en-US","title":"B"}]}`

	searchTwoApps = `{"apps":[
		{"name":"apps/com.example.a","packageName":"com.example.a","displayName":"A"},
		{"name":"apps/com.example.b","packageName":"com.example.b","displayName":"B"}
	]}`
)

// TestRun_discoversAndFindsDrift is the happy multi-app path: with no
// positional packages the sweep discovers via apps.search, reads both apps,
// and reports the drift on the one that has it.
func TestRun_discoversAndFindsDrift(t *testing.T) {
	rt := &auditRT{t: t, searchBody: searchTwoApps, apps: map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	}}
	rc, _ := newRC(t, rt)

	report, err := auditcmd.Run(rc, auditcmd.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.AppsAudited != 2 || report.Summary.AppsFailed != 0 {
		t.Fatalf("summary = %+v, want 2 audited / 0 failed", report.Summary)
	}
	if len(report.Ran.Apps) != 2 || report.Ran.Apps[0] != "com.example.a" {
		t.Errorf("ran.apps = %v, want both apps in sorted order", report.Ran.Apps)
	}
	if len(report.Ran.Checks) != len(audit.IDs()) {
		t.Errorf("ran.checks = %v, want every check", report.Ran.Checks)
	}

	byCheck := map[string][]string{}
	for _, f := range report.Findings {
		byCheck[f.Check] = append(byCheck[f.Check], f.Package)
	}
	for _, id := range []string{audit.CheckLingeringDrafts, audit.CheckEmptyReleaseNotes, audit.CheckLocaleDrift} {
		if pkgs := byCheck[id]; len(pkgs) != 1 || pkgs[0] != "com.example.a" {
			t.Errorf("check %s hit %v, want only com.example.a", id, pkgs)
		}
	}
	if pkgs := byCheck[audit.CheckNoProductionRelease]; len(pkgs) != 0 {
		t.Errorf("no-production-release hit %v, want nothing (both apps ship to production)", pkgs)
	}

	// Read-only proof: every Edit opened was discarded, and the transport
	// fatals on any other mutating verb.
	if rt.discards != 2 {
		t.Errorf("discarded %d Edits, want 2 (one per audited app)", rt.discards)
	}
	if len(rt.openEdit) != 0 {
		t.Errorf("Edits left open: %v", rt.openEdit)
	}
}

// TestRun_cleanAccount is the zero-findings path: the report still names what
// ran, so a clean bill is distinguishable from an empty sweep.
func TestRun_cleanAccount(t *testing.T) {
	rt := &auditRT{t: t, apps: map[string]appFixture{
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	}}
	rc, _ := newRC(t, rt)

	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.b"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", report.Findings)
	}
	if report.Summary.AppsAudited != 1 || len(report.Ran.Checks) == 0 {
		t.Errorf("ran section = %+v / %+v, want the audited app and the checks named", report.Ran, report.Summary)
	}
	// Explicit packages must not cost a Reporting call.
	for _, c := range rt.calls {
		if strings.Contains(c, "apps:search") {
			t.Errorf("named packages still hit apps.search: %v", rt.calls)
		}
	}
}

// TestRun_partialFailureDoesNotAbort covers the sweep-error path: one app the
// credential cannot read is reported inside the document, the others are still
// audited, and the failure carries its taxonomy exit code.
func TestRun_partialFailureDoesNotAbort(t *testing.T) {
	rt := &auditRT{t: t, apps: map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		"com.example.b": {status: http.StatusForbidden},
	}}
	rc, _ := newRC(t, rt)

	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
	if err != nil {
		t.Fatalf("Run: %v (a per-app failure must not abort the sweep)", err)
	}
	if report.Summary.AppsAudited != 1 || report.Summary.AppsFailed != 1 {
		t.Fatalf("summary = %+v, want 1 audited / 1 failed", report.Summary)
	}
	if len(report.Errors) != 1 || report.Errors[0].Package != "com.example.b" {
		t.Fatalf("errors = %+v, want the forbidden app", report.Errors)
	}
	if report.Errors[0].ExitCode != 11 {
		t.Errorf("error exitCode = %d, want 11 (403 authorization)", report.Errors[0].ExitCode)
	}
	if len(report.Ran.Apps) != 1 || report.Ran.Apps[0] != "com.example.a" {
		t.Errorf("ran.apps = %v, want only the app that was actually read", report.Ran.Apps)
	}
	// The failed app's Edit is still discarded: a dangling Edit would block
	// the operator's next publish for 24h.
	if rt.discards != 2 {
		t.Errorf("discarded %d Edits, want 2 (including the app that failed mid-read)", rt.discards)
	}
}

// TestRun_checkSelection asserts --check / --skip-check reach both the
// evaluation and the what-ran section, and that an unknown ID is CLI misuse.
func TestRun_checkSelection(t *testing.T) {
	fixtures := map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
	}

	t.Run("only the selected check runs", func(t *testing.T) {
		rc, _ := newRC(t, &auditRT{t: t, apps: fixtures})
		report, err := auditcmd.Run(rc, auditcmd.Input{
			Packages: []string{"com.example.a"},
			Checks:   []string{audit.CheckLingeringDrafts},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(report.Ran.Checks) != 1 || report.Ran.Checks[0] != audit.CheckLingeringDrafts {
			t.Fatalf("ran.checks = %v, want only lingering-drafts", report.Ran.Checks)
		}
		for _, f := range report.Findings {
			if f.Check != audit.CheckLingeringDrafts {
				t.Errorf("finding from a deselected check: %+v", f)
			}
		}
	})

	t.Run("skip-check removes it from the what-ran section", func(t *testing.T) {
		rc, _ := newRC(t, &auditRT{t: t, apps: fixtures})
		report, err := auditcmd.Run(rc, auditcmd.Input{
			Packages:   []string{"com.example.a"},
			SkipChecks: []string{audit.CheckEmptyReleaseNotes},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		for _, id := range report.Ran.Checks {
			if id == audit.CheckEmptyReleaseNotes {
				t.Fatalf("ran.checks still lists the skipped check: %v", report.Ran.Checks)
			}
		}
		for _, f := range report.Findings {
			if f.Check == audit.CheckEmptyReleaseNotes {
				t.Errorf("skipped check still produced a finding: %+v", f)
			}
		}
	})

	t.Run("unknown check is exit 2", func(t *testing.T) {
		rc, _ := newRC(t, &auditRT{t: t, apps: fixtures})
		_, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}, Checks: []string{"lingering-draft"}})
		if err == nil {
			t.Fatal("Run = nil error, want CLI misuse for an unknown check ID")
		}
		if code := exit.For(err); code != 2 {
			t.Errorf("exit code = %d, want 2 (CLI misuse)", code)
		}
	})

	t.Run("skipping every check is exit 2", func(t *testing.T) {
		rc, _ := newRC(t, &auditRT{t: t, apps: fixtures})
		_, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}, SkipChecks: audit.IDs()})
		if err == nil {
			t.Fatal("Run = nil error, want CLI misuse when the selection is empty")
		}
		if code := exit.For(err); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})
}

// TestExitCodes pins the three outcomes an automated caller branches on:
// clean (0), findings (70), and a sweep that could not run at all.
func TestExitCodes(t *testing.T) {
	t.Run("clean sweep exits 0 and hands the report to the kernel", func(t *testing.T) {
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
		}}
		rc, stdout := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.b"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		renderable, err := auditcmd.Emit(rc, report)
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		if code := exit.For(err); code != 0 {
			t.Errorf("clean exit = %d, want 0", code)
		}
		if renderable == nil {
			t.Error("Emit returned no Renderable on the clean path: the kernel would print nothing")
		}
		if stdout.Len() != 0 {
			t.Errorf("Emit wrote to stdout on the clean path (%q): the kernel renders there", stdout.String())
		}
	})

	t.Run("findings exit 70 with the report still on stdout", func(t *testing.T) {
		if exit.FindingsCode != 70 {
			t.Fatalf("FindingsCode = %d, want the documented 70 (DESIGN §9)", exit.FindingsCode)
		}
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		}}
		rc, stdout := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		renderable, err := auditcmd.Emit(rc, report)
		if err == nil {
			t.Fatal("Emit = nil error with findings present, want the exit-70 marker")
		}
		if code := exit.For(err); code != exit.FindingsCode {
			t.Errorf("findings exit = %d, want 70 (distinct from any error code)", code)
		}
		if renderable != nil {
			t.Error("Emit returned a Renderable AND an error: the kernel would not render it, so the report must go out here")
		}
		if !strings.Contains(stdout.String(), audit.CheckLingeringDrafts) {
			t.Errorf("report not written to stdout on the findings path: %q", stdout.String())
		}
	})

	// The regression this pins: a sweep where nothing could be read used to
	// return zero findings and exit 0, handing back a clean bill for an
	// account it never actually looked at.
	t.Run("every app failed exits with the API code, not 0", func(t *testing.T) {
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.a": {status: http.StatusForbidden},
			"com.example.b": {status: http.StatusForbidden},
		}}
		rc, stdout := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if report.Summary.AppsAudited != 0 || report.Summary.Findings != 0 {
			t.Fatalf("summary = %+v, want 0 audited / 0 findings", report.Summary)
		}
		renderable, err := auditcmd.Emit(rc, report)
		if err == nil {
			t.Fatal("Emit = nil error after a sweep that read nothing: that is a clean bill for an unlooked-at account")
		}
		if code := exit.For(err); code != 11 {
			t.Errorf("exit = %d, want 11 (the 403 both apps returned)", code)
		}
		if renderable != nil {
			t.Error("Emit returned a Renderable AND an error; the report must be written here instead")
		}
		if !strings.Contains(stdout.String(), "com.example.a") {
			t.Errorf("the report naming the unread apps was not written to stdout: %q", stdout.String())
		}
	})

	// Errors outrank findings: a hole in the sweep is not a result.
	t.Run("errors win over findings", func(t *testing.T) {
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
			"com.example.b": {status: http.StatusNotFound},
		}}
		rc, _ := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if report.Summary.Findings == 0 {
			t.Fatal("fixture produced no finding; the case is not exercising the precedence")
		}
		if _, err := auditcmd.Emit(rc, report); exit.For(err) != 30 {
			t.Errorf("exit = %d, want 30 (the unread app's 404), not 70", exit.For(err))
		}
	})

	// Non-retryable-wins, the `apps add` batch rule: a permanent failure must
	// not be masked by a transient one, or a caller loops on a 403 forever.
	t.Run("a permanent failure outranks a transient one", func(t *testing.T) {
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.a": {status: http.StatusServiceUnavailable},
			"com.example.b": {status: http.StatusForbidden},
		}}
		rc, _ := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := auditcmd.Emit(rc, report); exit.For(err) != 11 {
			t.Errorf("exit = %d, want 11: the permanent 403 wins over the retryable 5xx", exit.For(err))
		}
	})

	// The mirror image: when every failure is transient, the caller is told so.
	t.Run("all-transient failures keep a retryable code", func(t *testing.T) {
		rt := &auditRT{t: t, apps: map[string]appFixture{
			"com.example.a": {status: http.StatusServiceUnavailable},
		}}
		rc, _ := newRC(t, rt)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := auditcmd.Emit(rc, report); exit.For(err) != 40 {
			t.Errorf("exit = %d, want 40 (API 5xx, retry-safe)", exit.For(err))
		}
	})

	t.Run("a sweep that cannot discover fails with the API code", func(t *testing.T) {
		rt := &auditRT{t: t, searchBody: `{"apps":[]}`, apps: map[string]appFixture{}}
		rc, _ := newRC(t, rt)
		_, err := auditcmd.Run(rc, auditcmd.Input{})
		if err == nil {
			t.Fatal("Run = nil error, want a refusal when discovery yields no app")
		}
		if code := exit.For(err); code == 0 || code == 70 {
			t.Errorf("exit = %d, want a real error code (never 0, never 70)", code)
		}
	})
}

// TestReportJSONShape pins the gplay-owned document: the keys a CI job or an
// agent reads. Not an API pass-through (the report composes several
// resources), so gplay owns and must not silently reshape it.
func TestReportJSONShape(t *testing.T) {
	rt := &auditRT{t: t, apps: map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
	}}
	rc, _ := newRC(t, rt)
	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, auditcmd.Payload{Report: report}.Renderers()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc struct {
		Ran struct {
			Apps   []string `json:"apps"`
			Checks []string `json:"checks"`
		} `json:"ran"`
		Findings []struct {
			Package  string            `json:"package"`
			Check    string            `json:"check"`
			Severity string            `json:"severity"`
			Message  string            `json:"message"`
			Evidence map[string]string `json:"evidence"`
		} `json:"findings"`
		Summary struct {
			AppsAudited int `json:"appsAudited"`
			AppsFailed  int `json:"appsFailed"`
			Findings    int `json:"findings"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, buf.String())
	}
	if len(doc.Ran.Apps) != 1 || len(doc.Ran.Checks) != len(audit.IDs()) {
		t.Errorf("ran = %+v, want the audited app and every check", doc.Ran)
	}
	if doc.Summary.Findings != len(doc.Findings) || doc.Summary.Findings == 0 {
		t.Errorf("summary.findings = %d, findings = %d", doc.Summary.Findings, len(doc.Findings))
	}
	f := doc.Findings[0]
	if f.Package == "" || f.Check == "" || f.Severity == "" || f.Message == "" {
		t.Errorf("finding is missing a required field: %+v", f)
	}
}

// TestRun_dedupesPackages: naming the same package twice must not audit it
// twice, which would double the quota cost for no new information.
func TestRun_dedupesPackages(t *testing.T) {
	rt := &auditRT{t: t, apps: map[string]appFixture{
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	}}
	rc, _ := newRC(t, rt)
	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.b", "com.example.b", " "}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.AppsAudited != 1 {
		t.Errorf("audited %d apps, want 1", report.Summary.AppsAudited)
	}
	if rt.discards != 1 {
		t.Errorf("opened %d Edits, want 1", rt.discards)
	}
}
