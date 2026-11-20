package auditcmd_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/internal/audit"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

type appFixture struct {
	tracksBody   string
	listingsBody string
	status       int
}

func auditFake(t *testing.T, searchBody string, apps map[string]appFixture) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/apps:search"):
			return http.StatusOK, searchBody, true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, fmt.Sprintf(`{"id":"edit-%s","expiryTimeSeconds":"1700000000"}`, pkgOf(c.Path)), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/tracks"):
			f := apps[pkgOf(c.Path)]
			if f.status != 0 {
				return f.status, `{"error":{"message":"nope"}}`, true
			}
			return http.StatusOK, f.tracksBody, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return http.StatusOK, apps[pkgOf(c.Path)].listingsBody, true
		}
		t.Errorf("audit issued a non-read request (it must never mutate): %s %s", c.Method, c.URL)
		return 0, "", false
	})
}

func isDiscard(c testkit.Call) bool {
	return c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/")
}

func discards(fake *testkit.Fake) int {
	n := 0
	for _, c := range fake.Calls() {
		if isDiscard(c) {
			n++
		}
	}
	return n
}

func openEdits(fake *testkit.Fake) map[string]bool {
	open := map[string]bool{}
	for _, c := range fake.Calls() {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			open[pkgOf(c.Path)] = true
		case isDiscard(c):
			delete(open, pkgOf(c.Path))
		}
	}
	return open
}

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

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key := testkit.RSAKey(t)
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
	driftedTracks = `{"tracks":[
		{"track":"internal","releases":[{"name":"3.0.0","status":"draft","versionCodes":["30"]}]},
		{"track":"production","releases":[{"name":"2.0.0","status":"completed","versionCodes":["20"]}]}
	]}`
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

func TestRun_discoversAndFindsDrift(t *testing.T) {
	fake := auditFake(t, searchTwoApps, map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	})
	rc, _ := newRC(t, fake)

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

	if n := discards(fake); n != 2 {
		t.Errorf("discarded %d Edits, want 2 (one per audited app)", n)
	}
	if open := openEdits(fake); len(open) != 0 {
		t.Errorf("Edits left open: %v", open)
	}
}

func TestRun_cleanAccount(t *testing.T) {
	fake := auditFake(t, "", map[string]appFixture{
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	})
	rc, _ := newRC(t, fake)

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
	for _, c := range fake.Calls() {
		if strings.Contains(c.Path, "apps:search") {
			t.Errorf("named packages still hit apps.search: %s %s", c.Method, c.Path)
		}
	}
}

func TestRun_partialFailureDoesNotAbort(t *testing.T) {
	fake := auditFake(t, "", map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		"com.example.b": {status: http.StatusForbidden},
	})
	rc, _ := newRC(t, fake)

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
	if n := discards(fake); n != 2 {
		t.Errorf("discarded %d Edits, want 2 (including the app that failed mid-read)", n)
	}
}

func TestRun_checkSelection(t *testing.T) {
	fixtures := map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
	}

	t.Run("only the selected check runs", func(t *testing.T) {
		rc, _ := newRC(t, auditFake(t, "", fixtures))
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
		rc, _ := newRC(t, auditFake(t, "", fixtures))
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
		rc, _ := newRC(t, auditFake(t, "", fixtures))
		_, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}, Checks: []string{"lingering-draft"}})
		if err == nil {
			t.Fatal("Run = nil error, want CLI misuse for an unknown check ID")
		}
		if code := exit.For(err); code != 2 {
			t.Errorf("exit code = %d, want 2 (CLI misuse)", code)
		}
	})

	t.Run("skipping every check is exit 2", func(t *testing.T) {
		rc, _ := newRC(t, auditFake(t, "", fixtures))
		_, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}, SkipChecks: audit.IDs()})
		if err == nil {
			t.Fatal("Run = nil error, want CLI misuse when the selection is empty")
		}
		if code := exit.For(err); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})
}

func TestExitCodes(t *testing.T) {
	t.Run("clean sweep exits 0 and hands the report to the kernel", func(t *testing.T) {
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
		})
		rc, stdout := newRC(t, fake)
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
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
		})
		rc, stdout := newRC(t, fake)
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

	t.Run("every app failed exits with the API code, not 0", func(t *testing.T) {
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.a": {status: http.StatusForbidden},
			"com.example.b": {status: http.StatusForbidden},
		})
		rc, stdout := newRC(t, fake)
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

	t.Run("errors win over findings", func(t *testing.T) {
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
			"com.example.b": {status: http.StatusNotFound},
		})
		rc, _ := newRC(t, fake)
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

	t.Run("a permanent failure outranks a transient one", func(t *testing.T) {
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.a": {status: http.StatusServiceUnavailable},
			"com.example.b": {status: http.StatusForbidden},
		})
		rc, _ := newRC(t, fake)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a", "com.example.b"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := auditcmd.Emit(rc, report); exit.For(err) != 11 {
			t.Errorf("exit = %d, want 11: the permanent 403 wins over the retryable 5xx", exit.For(err))
		}
	})

	t.Run("all-transient failures keep a retryable code", func(t *testing.T) {
		fake := auditFake(t, "", map[string]appFixture{
			"com.example.a": {status: http.StatusServiceUnavailable},
		})
		rc, _ := newRC(t, fake)
		report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.a"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := auditcmd.Emit(rc, report); exit.For(err) != 40 {
			t.Errorf("exit = %d, want 40 (API 5xx, retry-safe)", exit.For(err))
		}
	})

	t.Run("a sweep that cannot discover fails with the API code", func(t *testing.T) {
		fake := auditFake(t, `{"apps":[]}`, map[string]appFixture{})
		rc, _ := newRC(t, fake)
		_, err := auditcmd.Run(rc, auditcmd.Input{})
		if err == nil {
			t.Fatal("Run = nil error, want a refusal when discovery yields no app")
		}
		if code := exit.For(err); code == 0 || code == 70 {
			t.Errorf("exit = %d, want a real error code (never 0, never 70)", code)
		}
	})
}

func TestReportJSONShape(t *testing.T) {
	fake := auditFake(t, "", map[string]appFixture{
		"com.example.a": {tracksBody: driftedTracks, listingsBody: oneLocale},
	})
	rc, _ := newRC(t, fake)
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

func TestRun_dedupesPackages(t *testing.T) {
	fake := auditFake(t, "", map[string]appFixture{
		"com.example.b": {tracksBody: cleanTracks, listingsBody: twoLocales},
	})
	rc, _ := newRC(t, fake)
	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: []string{"com.example.b", "com.example.b", " "}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Summary.AppsAudited != 1 {
		t.Errorf("audited %d apps, want 1", report.Summary.AppsAudited)
	}
	if n := discards(fake); n != 1 {
		t.Errorf("opened %d Edits, want 1", n)
	}
}
