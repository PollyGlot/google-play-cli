// Package list_test exercises `gplay releases list` at the kernel level:
// a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// promote/upload harness, but the transport FAILS on any PUT or :commit
// — a read-only listing must open, read (tracks.get), and discard the
// Edit, never write or commit it.
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

	"github.com/PollyGlot/google-play-cli/commands/releases/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// listRT terminates the OAuth2 /token exchange and routes the read-only
// list sequence: edits.insert, tracks.get, edits.delete. It deliberately
// has NO PUT or :commit branch: reaching one means the command tried to
// mutate or commit, which a read-only list must never do — so the
// transport fails the test.
type listRT struct {
	t            *testing.T
	editID       string
	trackGetResp string
	trackGetCode int // 0 → 200

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
		return jsonResp(200, fmt.Sprintf(`{"id":%q,"expiryTimeSeconds":"1700000000"}`, r.editID)), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tracks/"):
		code := r.trackGetCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.trackGetResp), nil
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

// TestRun_listsAllReleases_happyPath asserts the read-only vertical
// slice: /token precedes edits.insert, then tracks.get, then the Edit is
// DISCARDED (never committed). Every coexisting release on the track —
// draft, inProgress, halted, completed — comes back, and --output json
// is the raw tracks.get payload (ADR-0003 pass-through).
func TestRun_listsAllReleases_happyPath(t *testing.T) {
	raw := `{"track":"production","releases":[` +
		`{"name":"150-draft","status":"draft","versionCodes":["150"]},` +
		`{"name":"142","status":"inProgress","versionCodes":["142"],"userFraction":0.1},` +
		`{"name":"141","status":"halted","versionCodes":["141"],"userFraction":0.5},` +
		`{"name":"140","status":"completed","versionCodes":["140"],"userFraction":1.0,"releaseNotes":[{"language":"en-US","text":"x"},{"language":"fr-FR","text":"y"}]}` +
		`]}`
	rt := &listRT{t: t, editID: "edit-list", trackGetResp: raw}
	rc, _ := newRC(t, rt)

	r, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-list/tracks/production",
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
		t.Errorf("JSON output = %s\nwant raw tracks.get payload = %s", got, raw)
	}
}

// TestRun_unknownTrack_exit30WithHint asserts the AC: an unknown track
// (tracks.get 404) maps to exit 30 with a hint pointing the operator at
// `gplay tracks list`, and the opened Edit is still discarded.
func TestRun_unknownTrack_exit30WithHint(t *testing.T) {
	rt := &listRT{
		t:            t,
		editID:       "edit-404",
		trackGetCode: 404,
		trackGetResp: `{"error":{"code":404,"message":"Track not found."}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "bogus"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "tracks list") {
		t.Errorf("error %q, want a hint mentioning `gplay tracks list`", err.Error())
	}
	// The Edit must still be discarded even on the error path.
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-404") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after 404; calls = %v", rt.calls)
	}
}

// TestRun_missingTrack_exit2 asserts --track is required and the command
// short-circuits with a usage error before any HTTP call.
func TestRun_missingTrack_exit2(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_exit2 asserts a missing package (no --package
// and no pin) is a usage error before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_noAccount_exit10 asserts that with no resolved Account the
// command fails auth (exit 10) before any HTTP call — there is no
// dry-run path for a read-only listing.
func TestRun_noAccount_exit10(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}

// TestRun_unknownColumn_exit2 asserts an unknown --columns value is a CLI
// misuse caught before any HTTP call.
func TestRun_unknownColumn_exit2(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Package: "com.example.app", Track: "production", Columns: "name,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRenderTable_defaultColumns_showsEveryRelease asserts the table view
// renders one row per coexisting release with the documented default
// columns.
func TestRenderTable_defaultColumns_showsEveryRelease(t *testing.T) {
	p := list.Payload{
		Track: "production",
		Releases: []tracks.Release{
			{Name: "150-draft", Status: "draft", VersionCodes: []string{"150"}},
			{Name: "142", Status: "inProgress", VersionCodes: []string{"142"}, UserFraction: 0.1},
			{Name: "141", Status: "halted", VersionCodes: []string{"141"}, UserFraction: 0.5},
		},
		Columns: list.DefaultColumns,
	}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"draft", "inProgress", "halted", "150-draft", "142", "141", "0.1", "0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderTable_emptyReleases_printsFriendlyMessage asserts the table
// view does not emit a bare header for a track with no releases.
func TestRenderTable_emptyReleases_printsFriendlyMessage(t *testing.T) {
	p := list.Payload{Track: "alpha", Releases: nil, Columns: list.DefaultColumns}
	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	if !strings.Contains(buf.String(), "alpha") {
		t.Errorf("empty table output = %q, want it to name the track", buf.String())
	}
}

// TestRenderMarkdown_isMarkdownTable_respectsColumnsOverride asserts the
// markdown view is a GFM table (header + `---` separator) and that the
// --columns override restricts the columns rendered.
func TestRenderMarkdown_isMarkdownTable_respectsColumnsOverride(t *testing.T) {
	p := list.Payload{
		Track: "production",
		Releases: []tracks.Release{
			{Name: "142", Status: "inProgress", VersionCodes: []string{"142"}, UserFraction: 0.1},
		},
		Columns: []string{"name", "status"},
	}
	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "142") || !strings.Contains(out, "inProgress") {
		t.Errorf("markdown output = %q, want name+status cells", out)
	}
	// The override dropped versionCodes; its value (the lone code) must
	// not appear as a column. (The code 142 doubles as the release name
	// here, so assert on the column header instead.)
	if strings.Contains(strings.ToLower(out), "version") {
		t.Errorf("markdown output = %q, want no versionCodes column under --columns name,status", out)
	}
}

// TestRender_unknownColumnKey_doesNotPanic guards the exported Payload:
// Run validates --columns, but a directly-constructed Payload (or a
// future DefaultColumns drift) must not crash the renderers. An unknown
// key renders an empty cell, never a nil-func-call panic.
func TestRender_unknownColumnKey_doesNotPanic(t *testing.T) {
	p := list.Payload{
		Track:    "production",
		Releases: []tracks.Release{{Name: "1", Status: "completed", VersionCodes: []string{"1"}}},
		Columns:  []string{"name", "bogus"},
	}
	var tbl bytes.Buffer
	if err := p.Renderers().Table(&tbl); err != nil {
		t.Fatalf("Table render with unknown column: %v", err)
	}
	var md bytes.Buffer
	if err := p.Renderers().Markdown(&md); err != nil {
		t.Fatalf("Markdown render with unknown column: %v", err)
	}
	if !strings.Contains(tbl.String(), "1") {
		t.Errorf("table output = %q, want the known column's value", tbl.String())
	}
}
