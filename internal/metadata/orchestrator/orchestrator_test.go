// Package orchestrator_test drives the apply orchestrator against a fake
// http.RoundTripper (no /token exchange — Apply receives the *http.Client
// directly). It exercises the three paths that matter: the read-only
// dry-run, the --confirm gate, the atomic single-Edit publish, the no-op
// quota conservation, and the --prune deletegroup plus its defaultLanguage
// guard.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/orchestrator"
)

// fakeRT routes the androidpublisher Edit + listings sequence and records
// every request line. Canned online Listings come from listingsBody;
// defaultLanguage from detailsLang. failPatch / failDelete inject a 500 on
// a given locale to test atomicity. Any unexpected call fails the test.
type fakeRT struct {
	t             *testing.T
	editID        string
	listingsBody  string // edits.listings.list response
	detailsLang   string // defaultLanguage for edits.details.get
	failPatchLoc  string // locale whose PATCH returns 500
	failDeleteLoc string // locale whose DELETE returns 500

	mu        sync.Mutex
	calls     []string
	patchBody map[string]string // locale -> raw PATCH body received
}

func (r *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.patchBody == nil {
		r.patchBody = map[string]string{}
	}
	path := req.URL.Path
	r.calls = append(r.calls, req.Method+" "+path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(path, ":commit"):
		return resp(200, `{}`), nil
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/edits"):
		return resp(200, `{"id":"`+r.editID+`","expiryTimeSeconds":"1700000000"}`), nil
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/listings"):
		return resp(200, r.listingsBody), nil
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/details"):
		return resp(200, `{"defaultLanguage":"`+r.detailsLang+`","contactEmail":"x@y.z"}`), nil
	case req.Method == http.MethodPatch && strings.Contains(path, "/listings/"):
		loc := path[strings.LastIndex(path, "/")+1:]
		body, _ := io.ReadAll(req.Body)
		r.patchBody[loc] = string(body)
		if loc == r.failPatchLoc {
			return resp(500, `{"error":{"code":500,"message":"boom"}}`), nil
		}
		return resp(200, `{"language":"`+loc+`","patched":true}`), nil
	case req.Method == http.MethodDelete && strings.Contains(path, "/listings/"):
		loc := path[strings.LastIndex(path, "/")+1:]
		if loc == r.failDeleteLoc {
			return resp(500, `{"error":{"code":500,"message":"boom"}}`), nil
		}
		return resp(204, ``), nil
	case req.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
		return resp(204, ``), nil // Edit discard
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, path)
	return nil, nil
}

func resp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func (r *fakeRT) saw(method, substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, method+" ") && strings.Contains(c, substr) {
			return true
		}
	}
	return false
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
// lists, computes the diff, and discards — never commits, never patches.
func TestApply_dryRun_readsDiffsDiscards(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "New", "full", "Body")}
	rt := &fakeRT{t: t, editID: "e1",
		listingsBody: `{"listings":[{"language":"en-US","fullDescription":"Body"}]}`} // title create, full unchanged
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.Summary.Create != 1 || res.Diff.Summary.Unchanged != 1 {
		t.Errorf("summary = %+v, want Create 1 Unchanged 1", res.Diff.Summary)
	}
	if rt.saw("POST", ":commit") {
		t.Error("dry-run committed an Edit")
	}
	if rt.saw("PATCH", "/listings/") {
		t.Error("dry-run patched a listing")
	}
	if !rt.saw("DELETE", "/edits/e1") {
		t.Error("dry-run did not discard its read-only Edit")
	}
}

// TestApply_confirmRequired: a real apply without --confirm fails exit 3
// (safety flag required, docs/DESIGN.md §9 — NOT the generic usage exit 2,
// #408) before any HTTP, naming --confirm for the JSON envelope's requires[].
func TestApply_confirmRequired(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := &fakeRT{t: t, editID: "e1"}
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x"}) // DryRun=false, Confirm=false
	if code := exitCode(t, err); code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	var safety *exit.SafetyFlagError
	if !errors.As(err, &safety) || safety.Flag != "confirm" {
		t.Errorf("err = %v (%T), want *exit.SafetyFlagError naming \"confirm\"", err, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", rt.calls)
	}
}

// TestApply_confirmRequired_prune asserts the --prune variant of the refusal
// carries the same exit 3 + named flag, and states the destructive delete in
// its message (the two branches of the confirm gate are distinct strings).
func TestApply_confirmRequired_prune(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := &fakeRT{t: t, editID: "e1"}
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
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", rt.calls)
	}
}

// TestApply_validation_exit20: a char-limit overflow blocks before network.
func TestApply_validation_exit20(t *testing.T) {
	long := strings.Repeat("x", 31) // title limit is 30
	local := listing.Tree{"en-US": ml("en-US", "title", long, "full", "F")}
	rt := &fakeRT{t: t, editID: "e1"}
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if code := exitCode(t, err); code != 20 {
		t.Errorf("exit = %d, want 20", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls on validation failure, saw %v", rt.calls)
	}
}

// TestApply_publishesAtomically: a create + update across two locales are
// patched inside one Edit, then committed once. Patched bodies are returned.
func TestApply_publishesAtomically(t *testing.T) {
	local := listing.Tree{
		"en-US": ml("en-US", "title", "Hello", "full", "Long"), // title create, full unchanged
		"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc"),
	}
	rt := &fakeRT{t: t, editID: "e9",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","fullDescription":"Long"},` +
			`{"language":"fr-FR","title":"Salut","fullDescription":"Desc"}]}`} // en-US title create, fr-FR title update
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !rt.saw("PATCH", "/listings/en-US") || !rt.saw("PATCH", "/listings/fr-FR") {
		t.Errorf("expected PATCH on both locales; calls=%v", rt.calls)
	}
	if !rt.saw("POST", ":commit") {
		t.Error("expected exactly one commit")
	}
	if len(res.Patched) != 2 {
		t.Errorf("Patched = %v, want 2 locales", res.Patched)
	}
	// The en-US body must carry only the changed field (title) + language,
	// never the unchanged fullDescription (PATCH = missing≠empty).
	var enBody map[string]string
	_ = json.Unmarshal([]byte(rt.patchBody["en-US"]), &enBody)
	if enBody["title"] != "Hello" || enBody["language"] != "en-US" {
		t.Errorf("en-US patch body = %v, want title=Hello language=en-US", enBody)
	}
	if _, ok := enBody["fullDescription"]; ok {
		t.Errorf("en-US patch body leaked unchanged fullDescription: %v", enBody)
	}
}

// TestApply_atomicFailure_discardsZeroPublished: when the second locale's
// PATCH fails, the Edit auto-discards and nothing is committed.
func TestApply_atomicFailure_discardsZeroPublished(t *testing.T) {
	local := listing.Tree{
		"en-US": ml("en-US", "title", "Hello", "full", "Long"),
		"fr-FR": ml("fr-FR", "title", "Bonjour", "full", "Desc"),
	}
	rt := &fakeRT{t: t, editID: "eX", failPatchLoc: "fr-FR",
		listingsBody: `{"listings":[]}`} // both locales are creates
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err == nil {
		t.Fatal("expected an error when a locale PATCH fails")
	}
	if rt.saw("POST", ":commit") {
		t.Error("commit happened despite a failed PATCH — not atomic")
	}
	if !rt.saw("DELETE", "/edits/eX") {
		t.Error("Edit was not discarded after the failure")
	}
}

// TestApply_noChanges_discardsNoCommit: an apply whose diff is a no-op
// discards the Edit rather than committing an empty one (quota).
func TestApply_noChanges_discardsNoCommit(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := &fakeRT{t: t, editID: "e0",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`} // identical
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rt.saw("POST", ":commit") {
		t.Error("committed an empty Edit for a no-op apply")
	}
	if rt.saw("PATCH", "/listings/") {
		t.Error("patched on a no-op apply")
	}
	if !rt.saw("DELETE", "/edits/e0") {
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
	rt := &fakeRT{t: t, editID: "ep", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // unchanged
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`} // online-only -> prune
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !rt.saw("DELETE", "/listings/it-IT") {
		t.Errorf("expected DELETE on it-IT; calls=%v", rt.calls)
	}
	if !rt.saw("POST", ":commit") {
		t.Error("prune apply did not commit")
	}
	if len(res.Pruned) != 1 || res.Pruned[0] != "it-IT" {
		t.Errorf("Pruned = %v, want [it-IT]", res.Pruned)
	}
}

// TestApply_atomicFailure_deleteFails_discards: when a prune DELETE fails
// AFTER an earlier locale PATCH succeeded, the whole Edit auto-discards —
// the successful patch is rolled back with it, nothing is committed.
func TestApply_atomicFailure_deleteFails_discards(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "NewT", "full", "F")} // title update
	rt := &fakeRT{t: t, editID: "eD", detailsLang: "en-US", failDeleteLoc: "it-IT",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // update target
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`} // prune target (DELETE fails)
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if err == nil {
		t.Fatal("expected an error when a prune DELETE fails")
	}
	if !rt.saw("PATCH", "/listings/en-US") {
		t.Errorf("expected the en-US PATCH to have run before the failing DELETE; calls=%v", rt.calls)
	}
	if rt.saw("POST", ":commit") {
		t.Error("commit happened despite a failed DELETE — the earlier PATCH was not rolled back")
	}
	if !rt.saw("DELETE", "/edits/eD") {
		t.Error("Edit was not discarded after the DELETE failure")
	}
}

// TestApply_dryRunPrune_refusesDefaultLanguage: the defaultLanguage prune
// guard fires in dry-run too (before the user ever reaches --confirm), so a
// preview of a prune that would remove the default Listing fails exit 2.
func TestApply_dryRunPrune_refusesDefaultLanguage(t *testing.T) {
	local := listing.Tree{"it-IT": ml("it-IT", "title", "Ciao", "full", "Lunga")}
	rt := &fakeRT{t: t, editID: "edg", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // default, online-only -> would prune
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse to prune defaultLanguage, even in dry-run)", code)
	}
	if rt.saw("POST", ":commit") {
		t.Error("dry-run prune refusal should never commit")
	}
}

// TestApply_prune_refusesDefaultLanguage: --prune that would delete the
// defaultLanguage Listing is refused (exit 2), nothing committed/deleted.
func TestApply_prune_refusesDefaultLanguage(t *testing.T) {
	local := listing.Tree{"it-IT": ml("it-IT", "title", "Ciao", "full", "Lunga")}
	rt := &fakeRT{t: t, editID: "eg", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` + // default, online-only -> would prune
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}
	_, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse to prune defaultLanguage)", code)
	}
	if rt.saw("POST", ":commit") {
		t.Error("committed despite the defaultLanguage prune refusal")
	}
	if rt.saw("DELETE", "/listings/") {
		t.Error("deleted a listing despite the refusal")
	}
}

// TestApply_prune_emptyTreeRefused: --prune against an empty local tree is
// refused (exit 2) BEFORE any network — an empty tree would otherwise
// classify every online locale as a delete and wipe the app's Store
// presence (the classic mis-pointed --dir). Covers the real-apply path.
func TestApply_prune_emptyTreeRefused(t *testing.T) {
	rt := &fakeRT{t: t, editID: "e1"}
	_, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", Confirm: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse --prune on an empty tree)", code)
	}
	var want *orchestrator.EmptyTreePruneError
	if !errors.As(err, &want) {
		t.Errorf("err = %v (%T), want *EmptyTreePruneError", err, err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls (refused before network), saw %v", rt.calls)
	}
}

// TestApply_dryRunPrune_emptyTreeRefused: the empty-tree prune guard fires
// in dry-run too, so a preview never renders a delete-everything plan.
func TestApply_dryRunPrune_emptyTreeRefused(t *testing.T) {
	rt := &fakeRT{t: t, editID: "e1"}
	_, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if code := exitCode(t, err); code != 2 {
		t.Errorf("exit = %d, want 2 (refuse --prune on an empty tree in dry-run)", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected 0 HTTP calls, saw %v", rt.calls)
	}
}

// TestApply_emptyTreeNoPrune_isNoop: an empty tree WITHOUT --prune is a
// legitimate no-op (nothing on disk to upsert, nothing pruned) — it must
// NOT be refused. It lists, finds no changes, and discards.
func TestApply_emptyTreeNoPrune_isNoop(t *testing.T) {
	rt := &fakeRT{t: t, editID: "e0",
		listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`}
	res, err := orchestrator.Apply(context.Background(), client(rt), listing.Tree{},
		orchestrator.Opts{Package: "com.x", Confirm: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.HasChanges() {
		t.Error("empty tree without --prune should be a no-op")
	}
	if rt.saw("POST", ":commit") {
		t.Error("committed for an empty-tree no-op")
	}
}

// TestApply_dryRunPrune_showsDeleteNoExecute: dry-run + prune surfaces the
// delete op in the diff but performs no DELETE and no commit.
func TestApply_dryRunPrune_showsDeleteNoExecute(t *testing.T) {
	local := listing.Tree{"en-US": ml("en-US", "title", "T", "full", "F")}
	rt := &fakeRT{t: t, editID: "ed", detailsLang: "en-US",
		listingsBody: `{"listings":[` +
			`{"language":"en-US","title":"T","fullDescription":"F"},` +
			`{"language":"it-IT","title":"Ciao","fullDescription":"Lunga"}]}`}
	res, err := orchestrator.Apply(context.Background(), client(rt), local,
		orchestrator.Opts{Package: "com.x", DryRun: true, Prune: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Diff.Summary.Delete != 1 {
		t.Errorf("Delete summary = %d, want 1", res.Diff.Summary.Delete)
	}
	if rt.saw("DELETE", "/listings/") || rt.saw("POST", ":commit") {
		t.Errorf("dry-run prune executed a delete/commit; calls=%v", rt.calls)
	}
}
