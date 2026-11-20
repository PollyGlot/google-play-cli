package pull_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/metadata/pull"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func testListings() []listings.Listing {
	return []listings.Listing{
		{Language: "en-US", Title: "My App", ShortDescription: "short", FullDescription: "long desc", Video: ""},
		{Language: "fr-FR", Title: "Mon App", ShortDescription: "", FullDescription: "desc longue", Video: "https://youtu.be/x"},
	}
}

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

type pullRT struct {
	editID       string
	listingsResp string
	insertCode   int
	listingsCode int
	insertBody   string
}

func newFake(t *testing.T, r pullRT) *testkit.Fake {
	t.Helper()
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			body := r.insertBody
			if body == "" {
				body = fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)
			}
			return r.insertCode, body, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return r.listingsCode, r.listingsResp, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/") && !strings.Contains(c.Path, "/listings"):
			return 204, "", true
		}
		t.Errorf("unexpected request (pull must not write/commit Play): %s %s", c.Method, c.Path)
		return 0, "", false
	})
}

func calls(f *testkit.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

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

func TestRun_pullsEveryLocale_happyPath(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{editID: "edit-pull", listingsResp: listingsJSON(t, testListings())})
	rc, _ := newRC(t, rt)

	r, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on happy path")
	}

	if n := rt.TokenExchanges(); n != 1 {
		t.Errorf("token exchanges = %d, want 1; calls=%v", n, calls(rt))
	}
	if got := rt.Calls(); len(got) > 0 && got[0].Header.Get("Authorization") != "Bearer a.b.c" {
		t.Errorf("first API call Authorization = %q, want the exchanged bearer", got[0].Header.Get("Authorization"))
	}
	wantSequence := []string{
		"POST /androidpublisher/v3/applications/com.example.app/edits",
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-pull/listings",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-pull",
	}
	if len(rt.Calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.Calls()), calls(rt), len(wantSequence))
	}
	for i, want := range wantSequence {
		if calls(rt)[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls(rt)[i], want)
		}
	}

	if got := readFile(t, filepath.Join(dir, "en-US", "title.txt")); got != "My App\n" {
		t.Errorf("en-US title = %q, want %q", got, "My App\n")
	}
	if got := readFile(t, filepath.Join(dir, "en-US", "short_description.txt")); got != "short\n" {
		t.Errorf("en-US short_description = %q, want %q", got, "short\n")
	}
	if got := readFile(t, filepath.Join(dir, "en-US", "full_description.txt")); got != "long desc\n" {
		t.Errorf("en-US full_description = %q, want %q", got, "long desc\n")
	}

	if got := readFile(t, filepath.Join(dir, "fr-FR", "title.txt")); got != "Mon App\n" {
		t.Errorf("fr-FR title = %q, want %q", got, "Mon App\n")
	}
	if got := readFile(t, filepath.Join(dir, "fr-FR", "video.txt")); got != "https://youtu.be/x\n" {
		t.Errorf("fr-FR video = %q, want %q", got, "https://youtu.be/x\n")
	}
}

func TestRun_emptyOnlineField_writesNoFile(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{editID: "edit-empty", listingsResp: listingsJSON(t, testListings())})
	rc, _ := newRC(t, rt)

	if _, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if p := filepath.Join(dir, "en-US", "video.txt"); fileExists(t, p) {
		t.Errorf("en-US video.txt exists but online video was empty: %s", p)
	}
	if p := filepath.Join(dir, "fr-FR", "short_description.txt"); fileExists(t, p) {
		t.Errorf("fr-FR short_description.txt exists but online short was empty: %s", p)
	}
}

func TestRun_localLocaleAbsentOnline_isNotDeleted(t *testing.T) {
	dir := t.TempDir()
	preDir := filepath.Join(dir, "zz-ZZ")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("mkdir zz-ZZ: %v", err)
	}
	prePath := filepath.Join(preDir, "title.txt")
	if err := os.WriteFile(prePath, []byte("Local Only\n"), 0o644); err != nil {
		t.Fatalf("write zz-ZZ title: %v", err)
	}

	rt := newFake(t, pullRT{editID: "edit-keep", listingsResp: listingsJSON(t, testListings())})
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

func TestRun_pullThenRead_isNoOpInvariant(t *testing.T) {
	dir := t.TempDir()
	fixture := testListings()
	rt := newFake(t, pullRT{editID: "edit-inv", listingsResp: listingsJSON(t, fixture)})
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

func TestRun_noListings_emptySummaryNotError(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{editID: "edit-none", listingsResp: `{"listings":[],"kind":"androidpublisher#listingsListResponse"}`})
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

func TestRun_jsonSummaryShape(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{editID: "edit-json", listingsResp: listingsJSON(t, testListings())})
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

func TestRun_noAccount_exit10(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{})
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", calls(rt))
	}
}

func TestRun_missingPackage_exit2(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{})
	rc, _ := newRC(t, rt)
	_, err := pull.Run(rc, pull.Input{Dir: dir})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", calls(rt))
	}
}

func TestRun_unknownPackage_exit30WithHint(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"Application not found.","errors":[{"reason":"applicationNotFound"}]}}`,
	})
	rc, _ := newRC(t, rt)

	_, err := pull.Run(rc, pull.Input{Package: "com.example.unknown", Dir: dir})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "apps list") {
		t.Errorf("error %q, want a hint mentioning `gplay apps list`", err.Error())
	}
	for _, c := range calls(rt) {
		if strings.HasPrefix(c, "DELETE ") {
			t.Errorf("unexpected DELETE after failed insert; calls = %v", calls(rt))
		}
	}
}

func TestRun_forbidden_exit11WithHint(t *testing.T) {
	dir := t.TempDir()
	rt := newFake(t, pullRT{
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	})
	rc, _ := newRC(t, rt)

	_, err := pull.Run(rc, pull.Input{Package: "com.example.app", Dir: dir})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}
