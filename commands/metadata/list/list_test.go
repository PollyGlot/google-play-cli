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

	"github.com/PollyGlot/google-play-cli/commands/metadata/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
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

func rowByLocale(rows []list.ListingRow) map[string]list.ListingRow {
	m := make(map[string]list.ListingRow, len(rows))
	for _, r := range rows {
		m[r.Locale] = r
	}
	return m
}

type listRT struct {
	editID       string
	listingsResp string
	insertCode   int
	listingsCode int
	insertBody   string
}

func newFake(t *testing.T, r listRT) *testkit.Fake {
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
		t.Errorf("unexpected request (read-only list must not write/commit): %s %s", c.Method, c.Path)
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

func TestRun_listsEveryLocale_happyPath(t *testing.T) {
	raw := `{"listings":[` +
		`{"language":"en-US","title":"My App","shortDescription":"short","fullDescription":"long desc","video":""},` +
		`{"language":"fr-FR","title":"Mon App","shortDescription":"","fullDescription":"desc longue","video":"https://youtu.be/x"}` +
		`],"kind":"androidpublisher#listingsListResponse"}`
	rt := newFake(t, listRT{editID: "edit-list", listingsResp: raw})
	rc, _ := newRC(t, rt)

	r, err := list.Run(rc, list.Input{Package: "com.example.app"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-list/listings",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-list",
	}
	if len(rt.Calls()) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.Calls()), calls(rt), len(wantSequence))
	}
	for i, want := range wantSequence {
		if calls(rt)[i] != want {
			t.Errorf("call %d = %q, want %q", i, calls(rt)[i], want)
		}
	}

	var jsonOut bytes.Buffer
	if err := r.Renderers().JSON(&jsonOut); err != nil {
		t.Fatalf("JSON render: %v", err)
	}
	if got := strings.TrimSpace(jsonOut.String()); got != strings.TrimSpace(raw) {
		t.Errorf("JSON output = %s\nwant raw edits.listings.list payload = %s", got, raw)
	}
}

func TestBuildRows_recordsFieldSizes_treatsEmptyAsAbsent(t *testing.T) {
	rows := list.BuildRows(testListings())
	by := rowByLocale(rows)

	en, ok := by["en-US"]
	if !ok {
		t.Fatalf("en-US row missing: %+v", rows)
	}
	if got := en.Sizes[listing.Title]; got != 6 {
		t.Errorf("en-US title size = %d, want 6", got)
	}
	if got := en.Sizes[listing.ShortDescription]; got != 5 {
		t.Errorf("en-US short size = %d, want 5", got)
	}
	if got := en.Sizes[listing.FullDescription]; got != 9 {
		t.Errorf("en-US full size = %d, want 9", got)
	}
	if _, present := en.Sizes[listing.Video]; present {
		t.Errorf("en-US video should be absent (empty), got size entry %d", en.Sizes[listing.Video])
	}

	fr, ok := by["fr-FR"]
	if !ok {
		t.Fatalf("fr-FR row missing: %+v", rows)
	}
	if _, present := fr.Sizes[listing.ShortDescription]; present {
		t.Errorf("fr-FR short should be absent (empty), got size entry")
	}
	if got := fr.Sizes[listing.Video]; got != 18 {
		t.Errorf("fr-FR video size = %d, want 18", got)
	}
}

func TestRenderTable_oneRowPerLocale_headersAndSizes(t *testing.T) {
	p := list.Payload{Rows: list.BuildRows(testListings())}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"LOCALE", "TITLE", "SHORT_DESC", "FULL_DESC", "VIDEO"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing header %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"en-US", "fr-FR"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing locale %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "9") {
		t.Errorf("table missing en-US full-desc size 9:\n%s", out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	var enLine string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "en-US") {
			enLine = l
		}
	}
	if enLine == "" {
		t.Fatalf("no en-US row found in table:\n%s", out)
	}
}

func TestRenderMarkdown_isGFMTable(t *testing.T) {
	p := list.Payload{Rows: list.BuildRows(testListings())}

	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "en-US") || !strings.Contains(strings.ToUpper(out), "LOCALE") {
		t.Errorf("markdown output = %q, want locale column", out)
	}
}

func TestRun_noAccount_exit10(t *testing.T) {
	rt := newFake(t, listRT{})
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", calls(rt))
	}
}

func TestRun_missingPackage_exit2(t *testing.T) {
	rt := newFake(t, listRT{})
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.Calls()) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", calls(rt))
	}
}

func TestRun_unknownPackage_exit30WithHint(t *testing.T) {
	rt := newFake(t, listRT{
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"Application not found.","errors":[{"reason":"applicationNotFound"}]}}`,
	})
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.unknown"})
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
	rt := newFake(t, listRT{
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	})
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}

func TestRun_listingsListError_discardsEditAndPropagates(t *testing.T) {
	rt := newFake(t, listRT{
		editID:       "edit-err",
		listingsCode: 503,
		listingsResp: `{"error":{"code":503,"message":"Backend error"}}`,
	})
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 40 {
		t.Errorf("ExitCode() = %d, want 40", code)
	}
	sawDelete := false
	for _, c := range calls(rt) {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-err") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after a failed listing read; calls = %v", calls(rt))
	}
}
