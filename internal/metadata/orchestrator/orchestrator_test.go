// Package orchestrator_test drives the apply orchestrator against a testkit.Fake
// (no /token exchange: Apply receives the *http.Client
// directly). It exercises the three paths that matter: the read-only
// dry-run, the --confirm gate, the atomic single-Edit publish, the no-op
// quota conservation, and the --prune deletegroup plus its defaultLanguage
// guard.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// fakeRT configures the testkit.Fake that routes the androidpublisher Edit +
// listings sequence. Canned online Listings come from listingsBody;
// defaultLanguage from detailsLang. failPatchLoc / failDeleteLoc inject a 500
// on a given locale to test atomicity (failPatchLoc covers both Listing
// writes, PATCH and PUT); notFoundLoc answers 404 to either write on that
// locale. Any unexpected call fails the test.
type fakeRT struct {
	editID        string
	listingsBody  string // edits.listings.list response
	detailsLang   string // defaultLanguage for edits.details.get
	failPatchLoc  string // locale whose PATCH returns 500
	failDeleteLoc string // locale whose DELETE returns 500
	notFoundLoc   string // locale whose PATCH/PUT returns 404
}

func newFake(t *testing.T, r fakeRT) *testkit.Fake {
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
			if loc == r.failPatchLoc {
				return 500, `{"error":{"code":500,"message":"boom"}}`, true
			}
			if loc == r.notFoundLoc {
				return 404, `{"error":{"code":404,"message":"Listing for language '` + loc + `' not found."}}`, true
			}
			return 200, `{"language":"` + loc + `","written":"` + c.Method + `"}`, true
		case c.Method == http.MethodDelete && strings.Contains(path, "/listings/"):
			loc := path[strings.LastIndex(path, "/")+1:]
			if loc == r.failDeleteLoc {
				return 500, `{"error":{"code":500,"message":"boom"}}`, true
			}
			return 204, ``, true
		case c.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
			return 204, ``, true // Edit discard
		}
		t.Errorf("unexpected request: %s %s", c.Method, path)
		return 0, "", false
	})
}

// calls lists the recorded requests as "METHOD path" lines.
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

func client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

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

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var c interface{ ExitCode() int }
	if !asCoder(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode()", err, err)
	}
	return c.ExitCode()
}

func asCoder(err error, target *interface{ ExitCode() int }) bool {
	for err != nil {
		if c, ok := err.(interface{ ExitCode() int }); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestApply_dryRun_readsDiffsDiscards: dry-run opens a read-only Edit,
// lists, computes the diff, and discards: never commits, never patches.
func TestApply_dryRun_readsDiffsDiscards(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")}
	rt := newFake(t, fakeRT{editID: "e1",
		listingsBody: `{"listings":[{"language":"en-US","fullDescription":"Body"}]}`}) // title create, full unchanged
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.Summary.Create != 1 || res.Diff.Summary.Unchanged != 1 {
		t.Errorf("summary = %+v, want Create 1 Unchanged 1", res.Diff.Summary)
	}
	if saw(rt, "POST", ":commit") {
		t.Error("dry-run committed an Edit")
	}
	if saw(rt, "PATCH", "/listings/") {
		t.Error("dry-run patched a listing")
	}
	if !saw(rt, "DELETE", "/edits/e1") {
		t.Error("dry-run did not discard its read-only Edit")
	}
}

// TestApply_confirmRequired: a real apply without --confirm fails exit 3
// (safety flag required, docs/DESIGN.md §9, NOT the generic usage exit 2,
// #408) before any HTTP, naming --confirm for the JSON envelope's requires[].
func TestApply_confirmRequired(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := newFake(t, fakeRT{editID: "e1"})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x"}) // DryRun=false, Confirm=false
	if code := exitCode(t, err); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", calls(rt))
	}
}

// TestApply_confirmRequired_prune asserts the --prune variant of the refusal
// carries the same exit 3 + named flag, and states the destructive delete in
// its message (the two branches of the confirm gate are distinct strings).
func TestApply_confirmRequired_prune(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := newFake(t, fakeRT{editID: "e1"})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Prune: true}) // DryRun=false, Confirm=false
	if code := exitCode(t, err); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Fatalf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if !strings.Contains(err.Error(), "deletes online locales") {
		t.Errorf("err = %q, want the --prune message naming the deletion", err.Error())
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", calls(rt))
	}
}

// TestApply_validation_exit20: a char-limit overflow blocks before network.
func TestApply_validation_exit20(t *testing.T) {
	long := strings.Repeat("x", 31) // title limit is 30
	local := listing.Tree{"en-US": ml("en-US", "title", long, "full", "F")}
	rt := newFake(t, fakeRT{editID: "e1"})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if code := exitCode(t, err); code != 20 {
		t.Errorf("exit = %d, want 20", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls on validation failure, saw %v", calls(rt))
	}
}

// TestApply_publishesAtomically: a create + update across two locales are
// patched inside one Edit, then committed once. Patched bodies are returned.
func TestApply_publishesAtomically(t *testing.T) {
	local := listing.Tree{
		"en-US": ml("en-US", "title", "Hello", "full", "Long"), // title create, full unchanged
		"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc"),
	}
	rt := newFake(t, fakeRT{editID: "e9",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","fullDescription":"Long"},` +
			`{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`}) // en-US title create, fr-FR title update
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !saw(rt, "PATCH", "/listings/en-US") || !saw(rt, "PATCH", "/listings/fr-FR") {
		t.Errorf("expected PATCH on both locales; calls=%v", calls(rt))
	}
	// en-US carries a field-level `create` (title absent online) but the
	// locale is live: it must stay a PATCH, never a PUT that would blank
	// the unchanged fullDescription.
	if saw(rt, "PUT", "/listings/") {
		t.Errorf("a live locale was written with PUT; calls=%v", calls(rt))
	}
	if len(res.Created) != 0 {
		t.Errorf("Created = %v, want none (both locales are live)", res.Created)
	}
	if !saw(rt, "POST", ":commit") {
		t.Error("expected exactly one commit")
	}
	if len(res.Patched) != 2 {
		t.Errorf("Patched = %v, want 2 locales", res.Patched)
	}
	// The en-US body must carry only the changed field (title) + language,
	// never the unchanged fullDescription (PATCH = missing≠empty).
	var enBody map[string]string
	_ = json.Unmarshal([]byte(sentBody(rt, http.MethodPatch, "en-US")), &enBody)
	if enBody["title"] != "Hello" || enBody["language"] != "en-US" {
		t.Errorf("en-US patch body = %v, want title=Hello language=en-US", enBody)
	}
	if _, ok := enBody["fullDescription"]; ok {
		t.Errorf("en-US patch body leaked unchanged fullDescription: %v", enBody)
	}
}

// TestApply_atomicFailure_discardsZeroPublished: when the second locale's
// write fails, the Edit auto-discards and nothing is committed.
func TestApply_atomicFailure_discardsZeroPublished(t *testing.T) {
	local := listing.Tree{
		"en-US": ml("en-US", "title", "Hello", "full", "Long"),
		"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc"),
	}
	rt := newFake(t, fakeRT{editID: "eX", failPatchLoc: "fr-FR",
		listingsBody: `{"listings":[]}`}) // both locales are creates
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err == nil {
		t.Fatal("expected an error when a locale write fails")
	}
	if saw(rt, "POST", ":commit") {
		t.Error("commit happened despite a failed PATCH, not atomic")
	}
	if !saw(rt, "DELETE", "/edits/eX") {
		t.Error("Edit was not discarded after the failure")
	}
}

// TestApply_noChanges_discardsNoCommit: an apply whose diff is a no-op
// discards the Edit rather than committing an empty one (quota).
func TestApply_noChanges_discardsNoCommit(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := newFake(t, fakeRT{editID: "e0",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`}) // identical
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if saw(rt, "POST", ":commit") {
		t.Error("committed an empty Edit for a no-op apply")
	}
	if saw(rt, "PATCH", "/listings/") {
		t.Error("patched on a no-op apply")
	}
	if !saw(rt, "DELETE", "/edits/e0") {
		t.Error("no-op apply did not discard its Edit")
	}
	if res.Diff.HasChanges() {
		t.Error("HasChanges = true for an identical tree")
	}
}

// TestApply_prune_deletesOnlineOnly: --prune removes a locale live on Play
// but absent on disk, inside the same committed Edit.
func TestApply_prune_deletesOnlineOnly(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := newFake(t, fakeRT{editID: "ep", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // unchanged
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}) // online-only -> prune
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !saw(rt, "DELETE", "/listings/it-IT") {
		t.Errorf("expected DELETE on it-IT; calls=%v", calls(rt))
	}
	if !saw(rt, "POST", ":commit") {
		t.Error("prune apply did not commit")
	}
	if len(res.Pruned) != 1 || res.Pruned[0] != "it-IT" {
		t.Errorf("Pruned = %v, want [it-IT]", res.Pruned)
	}
}

// TestApply_atomicFailure_deleteFails_discards: when a prune DELETE fails
// AFTER an earlier locale PATCH succeeded, the whole Edit auto-discards:
// the successful patch is rolled back with it, nothing is committed.
func TestApply_atomicFailure_deleteFails_discards(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "NewT", "full", "F")} // title update
	rt := newFake(t, fakeRT{editID: "eD", detailsLang: "en-US", failDeleteLoc: "it-IT",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // update target
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}) // prune target (DELETE fails)
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if err == nil {
		t.Fatal("expected an error when a prune DELETE fails")
	}
	if !saw(rt, "PATCH", "/listings/en-US") {
		t.Errorf("expected the en-US PATCH to have run before the failing DELETE; calls=%v", calls(rt))
	}
	if saw(rt, "POST", ":commit") {
		t.Error("commit happened despite a failed DELETE: the earlier PATCH was not rolled back")
	}
	if !saw(rt, "DELETE", "/edits/eD") {
		t.Error("Edit was not discarded after the DELETE failure")
	}
}

// TestApply_dryRunPrune_refusesDefaultLanguage: the defaultLanguage prune
// guard fires in dry-run too (before the user ever reaches --confirm), so a
// preview of a prune that would remove the default Listing fails exit 2.
func TestApply_dryRunPrune_refusesDefaultLanguage(t *testing.T) {
	local := listing.Tree{"it-IT": ml("it-IT", "title", "Ciao", "full", "Lunga")}
	rt := newFake(t, fakeRT{editID: "edg", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // default, online-only -> would prune
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse to prune defaultLanguage, even in dry-run)", code)
	}
	if saw(rt, "POST", ":commit") {
		t.Error("dry-run prune refusal should never commit")
	}
}

// TestApply_prune_refusesDefaultLanguage: --prune that would delete the
// defaultLanguage Listing is refused (exit 2), nothing committed/deleted.
func TestApply_prune_refusesDefaultLanguage(t *testing.T) {
	local := listing.Tree{"it-IT": ml("it-IT", "title", "Ciao", "full", "Lunga")}
	rt := newFake(t, fakeRT{editID: "eg", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // default, online-only -> would prune
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse to prune defaultLanguage)", code)
	}
	if saw(rt, "POST", ":commit") {
		t.Error("committed despite the defaultLanguage prune refusal")
	}
	if saw(rt, "DELETE", "/listings/") {
		t.Error("deleted a listing despite the refusal")
	}
}

// TestApply_prune_emptyTreeRefused: --prune against an empty local tree is
// refused (exit 2) BEFORE any network: an empty tree would otherwise
// classify every online locale as a delete and wipe the app's Store
// presence (the classic mis-pointed --dir). Covers the real-apply path.
func TestApply_prune_emptyTreeRefused(t *testing.T) {
	rt := newFake(t, fakeRT{editID: "e1"})
	_, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse --prune on an empty tree)", code)
	}
	var want *orchestrator.EmptyTreePruneError
	if !errors.As(err, &want) {
		t.Errorf("err = %v (%T), want *EmptyTreePruneError", err, err)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls (refused before network), saw %v", calls(rt))
	}
}

// TestApply_dryRunPrune_emptyTreeRefused: the empty-tree prune guard fires
// in dry-run too, so a preview never renders a delete-everything plan.
func TestApply_dryRunPrune_emptyTreeRefused(t *testing.T) {
	rt := newFake(t, fakeRT{editID: "e1"})
	_, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse --prune on an empty tree in dry-run)", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", calls(rt))
	}
}

// TestApply_emptyTreeNoPrune_isNoop: an empty tree WITHOUT --prune is a
// legitimate no-op (nothing on disk to upsert, nothing pruned): it must
// NOT be refused. It lists, finds no changes, and discards.
func TestApply_emptyTreeNoPrune_isNoop(t *testing.T) {
	rt := newFake(t, fakeRT{editID: "e0",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`})
	res, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.HasChanges() {
		t.Error("empty tree without --prune should be a no-op")
	}
	if saw(rt, "POST", ":commit") {
		t.Error("committed for an empty-tree no-op")
	}
}

// TestApply_dryRunPrune_showsDeleteNoExecute: dry-run + prune surfaces the
// delete op in the diff but performs no DELETE and no commit.
func TestApply_dryRunPrune_showsDeleteNoExecute(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := newFake(t, fakeRT{editID: "ed", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` +
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`})
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.Summary.Delete != 1 {
		t.Errorf("Delete summary = %d, want 1", res.Diff.Summary.Delete)
	}
	if saw(rt, "DELETE", "/listings/") || saw(rt, "POST", ":commit") {
		t.Errorf("dry-run prune executed a delete/commit; calls=%v", calls(rt))
	}
}

// TestApply_newLocale_createdByPUT_liveLocalePatched is the #561 fix: a
// locale absent from the listings.list read is created with
// edits.listings.update (PUT) carrying its complete Listing, while a live
// locale keeps edits.listings.patch with only its changed fields.
func TestApply_newLocale_createdByPUT_liveLocalePatched(t *testing.T) {
	local := listing.Tree{
		"en-US": ml("en-US", "title", "New title", "full", "Long"), // live: title update, full unchanged
		"de-DE": ml("de-DE", "title", "Meine App", "short", "Kurz", "full", "Lange Beschreibung"),
	}
	rt := newFake(t, fakeRT{editID: "e561",
		listingsBody: `{"listings":[{"language":"en-US","title":"Old title","fullDescription":"Long"}]}`})
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !saw(rt, "PUT", "/applications/com.x/edits/e561/listings/de-DE") {
		t.Errorf("expected PUT on the new de-DE locale; calls=%v", calls(rt))
	}
	if saw(rt, "PATCH", "/listings/de-DE") {
		t.Errorf("new de-DE locale was PATCHed (Play answers 404); calls=%v", calls(rt))
	}
	if !saw(rt, "PATCH", "/listings/en-US") || saw(rt, "PUT", "/listings/en-US") {
		t.Errorf("live en-US must be PATCHed, never PUT; calls=%v", calls(rt))
	}
	if !saw(rt, "POST", ":commit") {
		t.Error("expected exactly one commit")
	}

	// The PUT body is the complete new Listing.
	var deBody map[string]string
	if err := json.Unmarshal([]byte(sentBody(rt, http.MethodPut, "de-DE")), &deBody); err != nil {
		t.Fatalf("de-DE PUT body is not JSON: %v (%q)", err, sentBody(rt, http.MethodPut, "de-DE"))
	}
	want := map[string]string{
		"language": "de-DE", "title": "Meine App",
		"shortDescription": "Kurz", "fullDescription": "Lange Beschreibung",
	}
	if len(deBody) != len(want) {
		t.Errorf("de-DE PUT body = %v, want %v", deBody, want)
	}
	for k, v := range want {
		if deBody[k] != v {
			t.Errorf("de-DE PUT body[%s] = %q, want %q", k, deBody[k], v)
		}
	}
	// The PATCH body still carries only the changed field.
	var enBody map[string]string
	_ = json.Unmarshal([]byte(sentBody(rt, http.MethodPatch, "en-US")), &enBody)
	if enBody["title"] != "New title" {
		t.Errorf("en-US patch body = %v, want title=New title", enBody)
	}
	if _, ok := enBody["fullDescription"]; ok {
		t.Errorf("en-US patch body leaked unchanged fullDescription: %v", enBody)
	}

	if len(res.Created) != 1 || res.Created[0] != "de-DE" {
		t.Errorf("Created = %v, want [de-DE]", res.Created)
	}
	if len(res.Patched) != 2 {
		t.Errorf("Patched = %v, want both written locales (pass-through bodies)", res.Patched)
	}
}

// TestApply_localeWrite404_isLocaleError: a 404 on a per-locale write comes
// back as a *orchestrator.LocaleError naming the language, with the
// *api.Error still reachable (exit 30 unchanged), and the Edit discarded.
func TestApply_localeWrite404_isLocaleError(t *testing.T) {
	local := listing.Tree{"de-DE": ml("de-DE", "title", "Meine App", "full", "Lang")}
	rt := newFake(t, fakeRT{editID: "e404", notFoundLoc: "de-DE",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`})
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	var locErr *orchestrator.LocaleError
	if !errors.As(err, &locErr) || locErr.Locale != "de-DE" {
		t.Fatalf("err = %v (%T), want *orchestrator.LocaleError for de-DE", err, err)
	}
	if code := exitCode(t, err); code != 30 {
		t.Errorf("exit = %d, want 30 (the wrapped 404)", code)
	}
	if saw(rt, "POST", ":commit") || !saw(rt, "DELETE", "/edits/e404") {
		t.Errorf("expected discard and no commit; calls=%v", calls(rt))
	}
}
