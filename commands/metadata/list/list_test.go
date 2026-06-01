// Package list_test exercises `gplay metadata list` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// `tracks list` harness, but routes edits.listings.list (the per-locale
// Store front read) instead of tracks.list. The transport FAILS on any
// PATCH, PUT, DELETE-on-listing, or :commit — a read-only summary opens,
// reads, and discards the Edit, never writes or commits it.
package list_test

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
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/metadata/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

// testListings is the canonical two-locale fixture: en-US has title, short,
// full but an empty video; fr-FR has title, full, and a video URL but an
// empty short description.
func testListings() []listings.Listing {
	return []listings.Listing{
		{Language: "en-US", Title: "My App", ShortDescription: "short", FullDescription: "long desc", Video: ""},
		{Language: "fr-FR", Title: "Mon App", ShortDescription: "", FullDescription: "desc longue", Video: "https://youtu.be/x"},
	}
}

// rowByLocale indexes BuildRows output by locale for assertion.
func rowByLocale(rows []list.ListingRow) map[string]list.ListingRow {
	m := make(map[string]list.ListingRow, len(rows))
	for _, r := range rows {
		m[r.Locale] = r
	}
	return m
}

// listRT terminates the OAuth2 /token exchange and routes the read-only
// listings sequence: edits.insert, listings.list (GET .../listings),
// edits.delete. It deliberately has NO PATCH/PUT/:commit branch and fails
// on a DELETE that targets a /listings/ path: reaching one means the
// command tried to mutate or commit, which a read-only list must never do
// — so the transport fails the test. A DELETE on the bare /edits/<id> path
// is the expected Edit discard.
type listRT struct {
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

func (r *listRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	r.t.Fatalf("unexpected request (read-only list must not write/commit): %s %s", req.Method, req.URL)
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

// TestRun_listsEveryLocale_happyPath is the tracer bullet: /token precedes
// edits.insert, then listings.list (GET .../listings, NOT
// /listings/<locale>), then the Edit is DISCARDED (never committed).
// --output json is the raw edits.listings.list payload (ADR-0003
// pass-through).
func TestRun_listsEveryLocale_happyPath(t *testing.T) {
	raw := `{"listings":[` +
		`{"language":"en-US","title":"My App","shortDescription":"short","fullDescription":"long desc","video":""},` +
		`{"language":"fr-FR","title":"Mon App","shortDescription":"","fullDescription":"desc longue","video":"https://youtu.be/x"}` +
		`],"kind":"androidpublisher#listingsListResponse"}`
	rt := &listRT{t: t, editID: "edit-list", listingsResp: raw}
	rc, _ := newRC(t, rt)

	r, err := list.Run(rc, list.Input{Package: "com.example.app"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-list/listings",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-list",
	}
	if len(rt.calls) != len(wantSequence) {
		t.Fatalf("got %d calls (%v), want %d", len(rt.calls), rt.calls, len(wantSequence))
	}
	for i, want := range wantSequence {
		if rt.calls[i] != want {
			t.Errorf("call %d = %q, want %q", i, rt.calls[i], want)
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

// TestBuildRows_recordsFieldSizes_treatsEmptyAsAbsent asserts the
// per-locale projection: a present field records its character length; an
// empty API field has no size entry (renders blank).
func TestBuildRows_recordsFieldSizes_treatsEmptyAsAbsent(t *testing.T) {
	rows := list.BuildRows(testListings())
	by := rowByLocale(rows)

	en, ok := by["en-US"]
	if !ok {
		t.Fatalf("en-US row missing: %+v", rows)
	}
	// "My App" = 6 chars, "short" = 5, "long desc" = 9, video empty -> absent.
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

	// fr-FR: short description empty -> absent; video present (18 chars).
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

// TestRenderTable_oneRowPerLocale_headersAndSizes asserts the table view
// renders the LOCALE + field headers, one row per locale, the per-field
// char counts, and a blank cell for an absent/empty field.
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
	// en-US full description = 9 chars; fr-FR video URL = 18 chars.
	if !strings.Contains(out, "9") {
		t.Errorf("table missing en-US full-desc size 9:\n%s", out)
	}

	// The en-US row's VIDEO cell is empty: the row must show fewer
	// non-blank trailing cells than fr-FR (which has a video). Assert the
	// en-US line does not contain the fr-FR video size.
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

// TestRenderMarkdown_isGFMTable asserts the markdown view is a GFM table
// (header + `---` separator) carrying the locale and field columns.
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

// TestRun_noAccount_exit10 asserts that with no resolved Account the
// command fails auth (exit 10) before any HTTP call.
func TestRun_noAccount_exit10(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_exit2 asserts a missing package (no --package and
// no pin) is a usage error before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{})
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
	rt := &listRT{
		t:          t,
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"Application not found.","errors":[{"reason":"applicationNotFound"}]}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.unknown"})
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
	rt := &listRT{
		t:          t,
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}

// TestRun_listingsListError_discardsEditAndPropagates asserts the
// read-only Edit is discarded even when the listings read itself fails
// after the Edit was opened, and the underlying status drives the exit
// code (5xx -> 40).
func TestRun_listingsListError_discardsEditAndPropagates(t *testing.T) {
	rt := &listRT{
		t:            t,
		editID:       "edit-err",
		listingsCode: 503,
		listingsResp: `{"error":{"code":503,"message":"Backend error"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 40 {
		t.Errorf("ExitCode() = %d, want 40", code)
	}
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-err") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after a failed listing read; calls = %v", rt.calls)
	}
}
