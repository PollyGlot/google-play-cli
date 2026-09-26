// Package apply_test drives `gplay metadata apply` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key (so one transport covers /token + the
// androidpublisher calls), and Run invoked directly. The local tree is
// written into t.TempDir() and passed via Input.Dir.
package apply_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// applyRT configures the testkit.Fake that routes the apply sequence; the
// Fake records every request line and each PATCH / PUT body. notFoundLoc
// makes either Listing write on that locale answer 404.
type applyRT struct {
	editID       string
	listingsBody string
	detailsLang  string
	notFoundLoc  string
}

func newFake(t *testing.T, r applyRT) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		path := c.Path
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(path, ":commit"):
			return 200, `{}`, true
		case c.Method == http.MethodPost && strings.HasSuffix(path, "/edits"):
			return 200, `{"id":"` + r.editID + `","expiryTimeSeconds":"1700000000"}`, true
		case c.Method == http.MethodGet && strings.HasSuffix(path, "/listings"):
			return 200, r.listingsBody, true
		case c.Method == http.MethodGet && strings.HasSuffix(path, "/details"):
			return 200, `{"defaultLanguage":"` + r.detailsLang + `","contactEmail":"x@y.z"}`, true
		case (c.Method == http.MethodPatch || c.Method == http.MethodPut) && strings.Contains(path, "/listings/"):
			loc := path[strings.LastIndex(path, "/")+1:]
			if loc == r.notFoundLoc {
				return 404, `{"error":{"code":404,"message":"Listing for language '` + loc + `' not found."}}`, true
			}
			return 200, `{"language":"` + loc + `","title":"echo"}`, true
		case c.Method == http.MethodDelete && strings.Contains(path, "/listings/"):
			return 204, "", true
		case c.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
			return 204, "", true
		}
		t.Errorf("unexpected request: %s %s", c.Method, path)
		return 0, "", false
	})
}

// calls lists the recorded API requests as "METHOD path" lines.
func calls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func saw(f *testkit.Fake, method, substr string) bool {
	for _, c := range calls(f) {
		if strings.HasPrefix(c, method+" ") && strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// sentBody returns the raw body of the last method write on locale's Listing.
func sentBody(f *testkit.Fake, method, locale string) string {
	var body string
	for _, c := range f.Calls() {
		if c.Method == method && strings.HasSuffix(c.Path, "/listings/"+locale) {
			body = string(c.Body)
		}
	}
	return body
}

func newRC(t *testing.T, rt http.RoundTripper) *kernel.RunContext {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode()", err, err)
	}
	return c.ExitCode()
}

// writeTree writes tr into a fresh temp dir and returns the path.
func writeTree(t *testing.T, tr listing.Tree) string {
	t.Helper()
	dir := t.TempDir()
	if err := tree.Write(dir, tr); err != nil {
		t.Fatalf("tree.Write: %v", err)
	}
	return dir
}

func ml(code string, fv ...string) listing.Listing {
	l := listing.NewListing(code)
	keys := map[string]listing.Field{
		"title": listing.Title, "short": listing.ShortDescription,
		"full": listing.FullDescription, "video": listing.Video,
	}
	for i := 0; i+1 < len(fv); i += 2 {
		l.Set(keys[fv[i]], fv[i+1])
	}
	return l
}

// TestRun_dryRun_jsonDiffSchema asserts dry-run emits the gplay diff schema
// {package, changes[], summary} and never commits or patches.
func TestRun_dryRun_jsonDiffSchema(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")})
	rt := newFake(t, applyRT{editID: "e1",
		listingsBody: `{"listings":[{"language":"en-US","fullDescription":"Body"}]}`}) // title create, full unchanged
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var got struct {
		Package string `json:"package"`
		Changes []struct {
			Locale, Field, Op string
		} `json:"changes"`
		Summary struct{ Create, Update int } `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("diff JSON did not parse: %v\n%s", err, buf.String())
	}
	if got.Package != "com.x" || got.Summary.Create != 1 {
		t.Errorf("diff = %+v, want package com.x, summary.create 1", got)
	}
	if saw(rt, "POST", ":commit") || saw(rt, "PATCH", "/listings/") {
		t.Errorf("dry-run mutated Play; calls=%v", calls(rt))
	}
}

// TestRun_applyWithoutConfirm_exit3 asserts a real apply refuses without
// --confirm, before opening any Edit, with exit 3 (safety flag required,
// docs/DESIGN.md §9, NOT the generic usage exit 2, #408).
func TestRun_applyWithoutConfirm_exit3(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := newFake(t, applyRT{editID: "e1"})
	rc := newRC(t, rt)

	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir})
	if code := exitCodeOf(t, err); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error %q should point at --dry-run", err.Error())
	}
	for _, c := range calls(rt) {
		if strings.HasPrefix(c, "POST /androidpublisher") {
			t.Errorf("opened an Edit despite missing --confirm; calls=%v", calls(rt))
		}
	}
}

// TestRun_applyConfirm_patchesAndCommits asserts a confirmed apply patches
// the changed locale, commits once, and --output json is the per-locale
// patch body.
func TestRun_applyConfirm_patchesAndCommits(t *testing.T) {
	dir := writeTree(t, listing.Tree{"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc")})
	rt := newFake(t, applyRT{editID: "e7",
		listingsBody: `{"listings":[{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`}) // title update
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !saw(rt, "PATCH", "/listings/fr-FR") || !saw(rt, "POST", ":commit") {
		t.Errorf("expected PATCH fr-FR + commit; calls=%v", calls(rt))
	}
	// PATCH body carries only the changed title + language (missing≠empty).
	var body map[string]string
	_ = json.Unmarshal([]byte(sentBody(rt, http.MethodPatch, "fr-FR")), &body)
	if body["title"] != "Bonjour" {
		t.Errorf("patch body = %v, want title=Bonjour", body)
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("apply JSON did not parse: %v\n%s", err, buf.String())
	}
	if _, ok := out["fr-FR"]; !ok {
		t.Errorf("apply JSON missing fr-FR patch body: %s", buf.String())
	}
}

// TestRun_pruneConfirm_deletesOnlineOnly asserts --prune deletes an
// online-only locale and reports it.
func TestRun_pruneConfirm_deletesOnlineOnly(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := newFake(t, applyRT{editID: "ep", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` +
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`})
	rc := newRC(t, rt)

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true, Prune: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !saw(rt, "DELETE", "/listings/it-IT") || !saw(rt, "POST", ":commit") {
		t.Errorf("expected DELETE it-IT + commit; calls=%v", calls(rt))
	}
	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if !strings.Contains(buf.String(), "it-IT") || !strings.Contains(buf.String(), "pruned") {
		t.Errorf("apply JSON should report the pruned it-IT: %s", buf.String())
	}
}

// TestRun_dirMissing_exit20 asserts an unreadable --dir is exit 20 before
// any network.
func TestRun_dirMissing_exit20(t *testing.T) {
	rt := newFake(t, applyRT{})
	rc := newRC(t, rt)
	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: "/nonexistent/metadata/xyz", DryRun: true})
	if code := exitCodeOf(t, err); code != 20 {
		t.Errorf("exit = %d, want 20", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls on dir error, saw %v", calls(rt))
	}
}

// TestRun_noPackage_exit2 and TestRun_noAccount_exit10 guard the pre-HTTP
// usage/auth gates.
func TestRun_noPackage_exit2(t *testing.T) {
	rt := newFake(t, applyRT{})
	rc := newRC(t, rt)
	_, err := apply.Run(rc, apply.Input{DryRun: true})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rt := newFake(t, applyRT{})
	rc := newRC(t, rt)
	rc.Account = nil
	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls before auth, saw %v", calls(rt))
	}
}

// TestRun_applyConfirm_emitsConfirmationOnStderr asserts a committed apply
// prints a single ✓ line on stderr (DESIGN §8) naming the package, alongside
// the stdout payload.
func TestRun_applyConfirm_emitsConfirmationOnStderr(t *testing.T) {
	dir := writeTree(t, listing.Tree{"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc")})
	rt := newFake(t, applyRT{editID: "e7",
		listingsBody: `{"listings":[{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`})
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "com.x") {
		t.Errorf("apply ✓ line wrong:\n%s", got)
	}
}

// TestRun_dryRun_noConfirmationOnStderr asserts --dry-run never emits a ✓.
func TestRun_dryRun_noConfirmationOnStderr(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")})
	rt := newFake(t, applyRT{editID: "e1",
		listingsBody: `{"listings":[]}`})
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	if _, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "✓") {
		t.Errorf("dry-run emitted a ✓ confirmation; stderr=%q", stderr.String())
	}
}

// TestRun_applyConfirm_createsNewLocaleWithPUT is the #561 scenario: the app
// has only en-US, the tree adds de-DE. The new locale is created through
// edits.listings.update (PUT, full body), reported as "created", and its
// response body is in the per-locale --output json.
func TestRun_applyConfirm_createsNewLocaleWithPUT(t *testing.T) {
	dir := writeTree(t, listing.Tree{
		"en-US": ml("en-US", "title", "T", "full", "F"),
		"de-DE": ml("de-DE", "title", "Meine App", "short", "Kurz", "full", "Lang"),
	})
	rt := newFake(t, applyRT{editID: "e561",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`})
	rc := newRC(t, rt)
	var stderr bytes.Buffer
	rc.Stderr = &stderr

	r, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !saw(rt, "PUT", "/applications/com.x/edits/e561/listings/de-DE") || saw(rt, "PATCH", "/listings/de-DE") {
		t.Errorf("expected PUT (not PATCH) on de-DE; calls=%v", calls(rt))
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(sentBody(rt, http.MethodPut, "de-DE")), &body)
	if body["title"] != "Meine App" || body["shortDescription"] != "Kurz" || body["fullDescription"] != "Lang" || body["language"] != "de-DE" {
		t.Errorf("de-DE PUT body = %v, want the complete Listing", body)
	}

	var tbl bytes.Buffer
	if err := r.Renderers().Table(&tbl); err != nil {
		t.Fatalf("table render: %v", err)
	}
	if !strings.Contains(tbl.String(), "de-DE") || !strings.Contains(tbl.String(), "created") {
		t.Errorf("table should report de-DE as created:\n%s", tbl.String())
	}
	var js bytes.Buffer
	if err := r.Renderers().JSON(&js); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(js.Bytes(), &out); err != nil {
		t.Fatalf("apply JSON did not parse: %v\n%s", err, js.String())
	}
	if _, ok := out["de-DE"]; !ok {
		t.Errorf("apply JSON missing de-DE body: %s", js.String())
	}
	if !strings.Contains(stderr.String(), "1 locale(s) created, 0 patched") {
		t.Errorf("stderr ✓ line should count the created locale: %q", stderr.String())
	}
}

// TestRun_listing404_namesLanguageNotPackage: a 404 on a per-locale Listing
// write names the language and no longer sends the operator to `gplay apps
// list` (#561). The exit code stays 30 (the wrapped *api.Error).
func TestRun_listing404_namesLanguageNotPackage(t *testing.T) {
	dir := writeTree(t, listing.Tree{"de-DE": ml("de-DE", "title", "Meine App", "full", "Lang")})
	rt := newFake(t, applyRT{editID: "e404", notFoundLoc: "de-DE",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`})
	rc := newRC(t, rt)

	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("exit = %d, want 30", code)
	}
	msg := err.Error()
	if !strings.Contains(msg, `language "de-DE"`) {
		t.Errorf("message should name the language: %s", msg)
	}
	if strings.Contains(msg, "apps list") || strings.Contains(msg, `package "com.x" not found`) {
		t.Errorf("message still blames the package: %s", msg)
	}
}

// TestRun_edit404_stillNamesPackage guards the other side: a 404 outside a
// per-locale call (here the Edit insert) keeps the package hint.
func TestRun_edit404_stillNamesPackage(t *testing.T) {
	dir := writeTree(t, listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")})
	rc := newRC(t, testkit.NewFake(testkit.Any(404, `{"error":{"code":404,"message":"Package not found: com.x."}}`)))

	_, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, Confirm: true})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("exit = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), `package "com.x" not found`) {
		t.Errorf("an Edit-level 404 should keep the package hint: %s", err)
	}
}
