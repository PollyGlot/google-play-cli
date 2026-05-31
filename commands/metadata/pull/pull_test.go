// Package pull_test exercises `gplay metadata pull` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the oauth2.HTTPClient
// context key, Run invoked directly, and the resulting Metadata tree written
// into t.TempDir(). Mirrors the `metadata list` harness, but pull projects the
// online Listings onto the managed-field model (non-empty values only) and
// writes them to disk via internal/metadata/tree.Write — never committing the
// read-only Edit. The transport FAILS on any PATCH, PUT, DELETE-on-listing, or
// :commit: pull is a read-then-write-to-disk, never a write-to-Play.
package pull_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/metadata/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// testListings is the canonical two-locale fixture: en-US has title, short,
// full but an empty video; fr-FR has title, full, and a video URL but an empty
// short description.
func testListings() []listings.Listing {
	return []listings.Listing{
		{Language: "en-US", Title: "My App", ShortDescription: "short", FullDescription: "long desc", Video: ""},
		{Language: "fr-FR", Title: "Mon App", ShortDescription: "", FullDescription: "desc longue", Video: "https://youtu.be/x"},
	}
}

// listingsJSON renders an edits.listings.list envelope from a fixture slice so
// the transport can return exactly what the API would.
func listingsJSON(t *testing.T, ls []listings.Listing) string {
	t.Helper()
	type wire struct {
		Language         string `json:"language"`
		Title            string `json:"title"`
		FullDescription  string `json:"fullDescription"`
		ShortDescription string `json:"shortDescription"`
		Video            string `json:"video"`
	}
	env := struct {
		Listings []wire `json:"listings"`
		Kind     string `json:"kind"`
	}{Kind: "androidpublisher#listingsListResponse"}
	for _, l := range ls {
		env.Listings = append(env.Listings, wire{
			Language: l.Language, Title: l.Title, FullDescription: l.FullDescription,
			ShortDescription: l.ShortDescription, Video: l.Video,
		})
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal listings fixture: %v", err)
	}
	return string(b)
}

// pullRT terminates the OAuth2 /token exchange and routes the read-only
// listings sequence: edits.insert, listings.list (GET .../listings),
// edits.delete. It deliberately has NO PATCH/PUT/:commit branch and fails on a
// DELETE that targets a /listings/ path: reaching one means pull tried to
// mutate or commit Play, which it must never do. A DELETE on the bare
// /edits/<id> path is the expected read-only Edit discard.
type pullRT struct {
	t            *testing.T
	editID       string
	listingsResp string
	insertCode   int // 0 -> 200
	listingsCode int // 0 -> 200
	insertBody   string

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *pullRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}

	r.calls = append(r.calls, req.Method+" "+req.URL.Path)

	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		code := r.insertCode
		if code == 0 {
			code = 200
		}
		body := r.insertBody
		if body == "" {
			body = fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)
		}
		return jsonResp(code, body), nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/listings"):
		code := r.listingsCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.listingsResp), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/") && !strings.Contains(req.URL.Path, "/listings"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected request (pull must not write/commit Play): %s %s", req.Method, req.URL)
	return nil, nil
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

// readFile returns a field file's contents, failing the test if it cannot be
// read.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// fileExists reports whether path exists on disk.
func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}

// TestRun_pullsEveryLocale_happyPath is the tracer bullet: /token precedes
// edits.insert, then listings.list (GET .../listings), then the Edit is
// DISCARDED (never committed). Each non-empty online field is written to
// <dir>/<locale>/<field>.txt with its value + trailing newline; an empty
// online field writes no file.
func TestRun_pullsEveryLocale_happyPath(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t, editID: "edit-pull", listingsResp: listingsJSON(t, testListings())}
	rc, _ := newRC(t, rt)

	r, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if rt.tokenHits == 0 {
		t.Errorf("RoundTripper saw no /token exchange; calls=%v", rt.calls)
	}
	wantSequence := []string{
		"POST /token",
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-pull/listings",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-pull",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
		}
	}

	// en-US: title, short_description, full_description present (video empty).
	if got := readFile(t, filepath.Join(dir, "en-US", "title.txt")); got != "My App\n" {
		t.Errorf("en-US title = %q, want %q", got, "My App\n")
	}
	if got := readFile(t, filepath.Join(dir, "en-US", "short_description.txt")); got != "short\n" {
		t.Errorf("en-US short_description = %q, want %q", got, "short\n")
	}
	if got := readFile(t, filepath.Join(dir, "en-US", "full_description.txt")); got != "long desc\n" {
		t.Errorf("en-US full_description = %q, want %q", got, "long desc\n")
	}

	// fr-FR: title, full_description, video present (short empty).
	if got := readFile(t, filepath.Join(dir, "fr-FR", "title.txt")); got != "Mon App\n" {
		t.Errorf("fr-FR title = %q, want %q", got, "Mon App\n")
	}
	if got := readFile(t, filepath.Join(dir, "fr-FR", "video.txt")); got != "https://youtu.be/x\n" {
		t.Errorf("fr-FR video = %q, want %q", got, "https://youtu.be/x\n")
	}
}

// TestRun_emptyOnlineField_writesNoFile asserts the ADR-0011 missing ≠ empty
// rule for the pull direction: a field Play holds empty ("") produces NO file
// on disk (not a 0-byte file), so a later apply leaves it untouched.
func TestRun_emptyOnlineField_writesNoFile(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t, editID: "edit-empty", listingsResp: listingsJSON(t, testListings())}
	rc, _ := newRC(t, rt)

	if _, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// en-US video is empty online -> no video.txt.
	if p := filepath.Join(dir, "en-US", "video.txt"); fileExists(t, p) {
		t.Errorf("en-US video.txt exists but online video was empty: %s", p)
	}
	// fr-FR short description is empty online -> no short_description.txt.
	if p := filepath.Join(dir, "fr-FR", "short_description.txt"); fileExists(t, p) {
		t.Errorf("fr-FR short_description.txt exists but online short was empty: %s", p)
	}
}

// TestRun_localLocaleAbsentOnline_isNotDeleted asserts pull's additive
// contract: a locale that exists only on disk (absent online) is left intact —
// pull never deletes from the local tree.
func TestRun_localLocaleAbsentOnline_isNotDeleted(t *testing.T) {
	dir := t.TempDir()
	// Pre-create a local-only locale not present in the online fixture.
	preDir := filepath.Join(dir, "zz-ZZ")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("mkdir zz-ZZ: %v", err)
	}
	prePath := filepath.Join(preDir, "title.txt")
	if err := os.WriteFile(prePath, []byte("Local Only\n"), 0o644); err != nil {
		t.Fatalf("write zz-ZZ title: %v", err)
	}

	rt := &pullRT{t: t, editID: "edit-keep", listingsResp: listingsJSON(t, testListings())}
	rc, _ := newRC(t, rt)

	if _, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !fileExists(t, prePath) {
		t.Errorf("local-only locale zz-ZZ was deleted by pull: %s gone", prePath)
	}
	if got := readFile(t, prePath); got != "Local Only\n" {
		t.Errorf("zz-ZZ title mutated by pull: got %q, want %q", got, "Local Only\n")
	}
}

// TestRun_pullThenRead_isNoOpInvariant proves the ADR-0011 invariant: after a
// pull, re-reading the on-disk tree (tree.Read) yields EXACTLY the non-empty
// projection of the online Listings (BuildTree). Equality field-by-field means
// a later `apply` sees no delta.
func TestRun_pullThenRead_isNoOpInvariant(t *testing.T) {
	dir := t.TempDir()
	fixture := testListings()
	rt := &pullRT{t: t, editID: "edit-inv", listingsResp: listingsJSON(t, fixture)}
	rc, _ := newRC(t, rt)

	if _, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	onDisk, err := tree.Read(dir)
	if err != nil {
		t.Fatalf("tree.Read after pull: %v", err)
	}
	want := pull.BuildTree(fixture)

	if len(onDisk) != len(want) {
		t.Fatalf("tree locale count = %d, want %d (onDisk=%v want=%v)",
			len(onDisk), len(want), onDisk.Locales(), want.Locales())
	}
	for _, locale := range want.Locales() {
		gotL, ok := onDisk[locale]
		if !ok {
			t.Errorf("locale %q missing from re-read tree", locale)
			continue
		}
		wantL := want[locale]
		if len(gotL.Fields) != len(wantL.Fields) {
			t.Errorf("locale %q field count = %d, want %d (got=%v want=%v)",
				locale, len(gotL.Fields), len(wantL.Fields), gotL.Fields, wantL.Fields)
			continue
		}
		for _, f := range listing.Fields() {
			wantV, wantManaged := wantL.Get(f)
			gotV, gotManaged := gotL.Get(f)
			if wantManaged != gotManaged {
				t.Errorf("locale %q field %v managed = %v, want %v", locale, f, gotManaged, wantManaged)
				continue
			}
			if gotV != wantV {
				t.Errorf("locale %q field %v = %q, want %q", locale, f, gotV, wantV)
			}
		}
	}
}

// TestBuildTree_excludesEmptyKeepsNonEmpty is the pure projection test: an
// empty online field is NOT managed (absent from the Listing), a non-empty one
// IS managed with its value. This is the structural source of the no-op
// invariant, verifiable with no I/O.
func TestBuildTree_excludesEmptyKeepsNonEmpty(t *testing.T) {
	tr := pull.BuildTree(testListings())

	en, ok := tr["en-US"]
	if !ok {
		t.Fatalf("en-US missing from BuildTree output: %v", tr.Locales())
	}
	if v, managed := en.Get(listing.Title); !managed || v != "My App" {
		t.Errorf("en-US title = (%q, %v), want (%q, true)", v, managed, "My App")
	}
	if v, managed := en.Get(listing.ShortDescription); !managed || v != "short" {
		t.Errorf("en-US short = (%q, %v), want (%q, true)", v, managed, "short")
	}
	if _, managed := en.Get(listing.Video); managed {
		t.Errorf("en-US video should be unmanaged (empty online), got managed")
	}

	fr, ok := tr["fr-FR"]
	if !ok {
		t.Fatalf("fr-FR missing from BuildTree output: %v", tr.Locales())
	}
	if _, managed := fr.Get(listing.ShortDescription); managed {
		t.Errorf("fr-FR short should be unmanaged (empty online), got managed")
	}
	if v, managed := fr.Get(listing.Video); !managed || v != "https://youtu.be/x" {
		t.Errorf("fr-FR video = (%q, %v), want (%q, true)", v, managed, "https://youtu.be/x")
	}
}

// TestBuildTree_allFieldsEmpty_dropsLocale asserts a locale whose every online
// field is empty contributes no Tree entry at all (no empty directory).
func TestBuildTree_allFieldsEmpty_dropsLocale(t *testing.T) {
	tr := pull.BuildTree([]listings.Listing{
		{Language: "de-DE", Title: "", ShortDescription: "", FullDescription: "", Video: ""},
	})
	if _, ok := tr["de-DE"]; ok {
		t.Errorf("de-DE has all-empty fields online but appears in the tree: %v", tr.Locales())
	}
	if len(tr) != 0 {
		t.Errorf("tree = %v, want empty (all fields empty online)", tr.Locales())
	}
}

// TestRun_noListings_emptySummaryNotError asserts that an app with zero online
// Listings is not an error: pull succeeds with an empty summary and writes
// nothing.
func TestRun_noListings_emptySummaryNotError(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t, editID: "edit-none", listingsResp: `{"listings":[],"kind":"androidpublisher#listingsListResponse"}`}
	rc, _ := newRC(t, rt)

	r, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run with no listings should not error: %v", err)
	}
	p, ok := r.(pull.Payload)
	if !ok {
		t.Fatalf("Run returned %T, want pull.Payload", r)
	}
	if p.Summary.Locales != 0 || p.Summary.Files != 0 {
		t.Errorf("summary = %+v, want zero locales/files", p.Summary)
	}
	if len(p.Pulled) != 0 {
		t.Errorf("pulled = %+v, want empty", p.Pulled)
	}
}

// TestRun_jsonSummaryShape asserts the gplay summary JSON shape: package, dir,
// pulled[] with API camelCase field keys, and the summary totals — NOT an API
// pass-through.
func TestRun_jsonSummaryShape(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t, editID: "edit-json", listingsResp: listingsJSON(t, testListings())}
	rc, _ := newRC(t, rt)

	r, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	if err := r.Renderers().JSON(&buf); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	var got struct {
		Package string `json:"package"`
		Dir     string `json:"dir"`
		Pulled  []struct {
			Locale string   `json:"locale"`
			Fields []string `json:"fields"`
		} `json:"pulled"`
		Summary struct {
			Locales int `json:"locales"`
			Files   int `json:"files"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal JSON output: %v\n%s", err, buf.String())
	}
	if got.Package != "com.example.app" {
		t.Errorf("package = %q, want com.example.app", got.Package)
	}
	if got.Dir != dir {
		t.Errorf("dir = %q, want %q", got.Dir, dir)
	}
	if got.Summary.Locales != 2 {
		t.Errorf("summary.locales = %d, want 2", got.Summary.Locales)
	}
	// en-US: title, shortDescription, fullDescription = 3; fr-FR: title,
	// fullDescription, video = 3 -> 6 files.
	if got.Summary.Files != 6 {
		t.Errorf("summary.files = %d, want 6", got.Summary.Files)
	}
	byLocale := map[string][]string{}
	for _, pr := range got.Pulled {
		byLocale[pr.Locale] = pr.Fields
	}
	wantEN := []string{"title", "shortDescription", "fullDescription"}
	if got := byLocale["en-US"]; !equalStrings(got, wantEN) {
		t.Errorf("en-US fields = %v, want %v (API camelCase keys, canonical order)", got, wantEN)
	}
	wantFR := []string{"title", "fullDescription", "video"}
	if got := byLocale["fr-FR"]; !equalStrings(got, wantFR) {
		t.Errorf("fr-FR fields = %v, want %v", got, wantFR)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRun_noAccount_exit10 asserts that with no resolved Account the command
// fails auth (exit 10) before any HTTP call.
func TestRun_noAccount_exit10(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_exit2 asserts a missing package (no --package and no
// pin) is a usage error before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := pull.Run(rc, pull.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_unknownPackage_exit30WithHint asserts that an unknown package
// (edits.insert 404) maps to exit 30 with a hint pointing the operator at
// `gplay apps list`, and that no Edit was opened (no DELETE).
func TestRun_unknownPackage_exit30WithHint(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{
		t:          t,
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"Application not found.","errors":[{"reason":"applicationNotFound"}]}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := pull.Run(rc, pull.Input{Package: "com.example.unknown", Dir: dir})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "apps list") {
		t.Errorf("error %q, want a hint mentioning `gplay apps list`", err.Error())
	}
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") {
			t.Errorf("unexpected DELETE after failed insert; calls = %v", rt.calls)
		}
	}
}

// TestRun_forbidden_exit11WithHint asserts that a 403 (service account not
// invited on the app) maps to exit 11 with the standard grant-access hint.
func TestRun_forbidden_exit11WithHint(t *testing.T) {
	dir := t.TempDir()
	rt := &pullRT{
		t:          t,
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}
