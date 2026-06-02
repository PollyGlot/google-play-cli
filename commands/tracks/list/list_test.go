// Package list_test exercises `gplay tracks list` at the kernel level: a
// RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// `releases list` harness, but routes edits.tracks.list (the cross-track
// read) instead of tracks.get. The transport FAILS on any PUT or :commit
// — a read-only listing opens, reads, and discards the Edit, never
// writes or commits it.
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

	"github.com/PollyGlot/google-play-cli/commands/tracks/list"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// rowByTrack indexes BuildRows output by track name for assertion.
func rowByTrack(rows []list.TrackRow) map[string]list.TrackRow {
	m := make(map[string]list.TrackRow, len(rows))
	for _, r := range rows {
		m[r.Track] = r
	}
	return m
}

// listRT terminates the OAuth2 /token exchange and routes the read-only
// tracks-list sequence: edits.insert, tracks.list (GET .../tracks),
// edits.delete. It deliberately has NO PUT or :commit branch: reaching
// one means the command tried to mutate or commit, which a read-only
// list must never do — so the transport fails the test.
type listRT struct {
	t          *testing.T
	editID     string
	tracksResp string
	insertCode int // 0 -> 200
	tracksCode int // 0 -> 200
	insertBody string

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
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/tracks"):
		code := r.tracksCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.tracksResp), nil
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

// TestRun_listsEveryTrack_happyPath is the tracer bullet: /token precedes
// edits.insert, then tracks.list (GET .../tracks, NOT /tracks/<name>),
// then the Edit is DISCARDED (never committed). --output json is the raw
// edits.tracks.list payload (ADR-0003 pass-through).
func TestRun_listsEveryTrack_happyPath(t *testing.T) {
	raw := `{"tracks":[` +
		`{"track":"production","releases":[{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0}]},` +
		`{"track":"qa-closed","releases":[{"name":"99","status":"completed","versionCodes":["99"]}]}` +
		`]}`
	rt := &listRT{t: t, editID: "edit-list", tracksResp: raw}
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-list/tracks",
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
		t.Errorf("JSON output = %s\nwant raw edits.tracks.list payload = %s", got, raw)
	}
}

// TestBuildRows_injectsAbsentStandardTracks_andMarksKind asserts the
// cross-track synthesis: the four standard tracks always appear (even
// when the API omits them), in canonical order, marked kind=standard;
// custom tracks the API returns appear after them marked kind=custom.
func TestBuildRows_injectsAbsentStandardTracks_andMarksKind(t *testing.T) {
	// API returns only production and a custom closed track — alpha,
	// beta, internal were never configured.
	api := []tracks.Track{
		{Track: "production", Releases: []tracks.Release{{Name: "142", Status: "completed", VersionCodes: []string{"142"}, UserFraction: 1.0}}},
		{Track: "qa-closed", Releases: []tracks.Release{{Name: "99", Status: "completed", VersionCodes: []string{"99"}}}},
	}
	rows := list.BuildRows(api)

	by := rowByTrack(rows)
	for _, std := range []string{"internal", "alpha", "beta", "production"} {
		r, ok := by[std]
		if !ok {
			t.Errorf("standard track %q missing from rows", std)
			continue
		}
		if r.Kind != "standard" {
			t.Errorf("track %q kind = %q, want standard", std, r.Kind)
		}
	}
	if r, ok := by["qa-closed"]; !ok || r.Kind != "custom" {
		t.Errorf("custom track qa-closed = %+v (present=%v), want kind=custom", r, ok)
	}

	// Standard tracks come first, in canonical order, then custom.
	wantOrder := []string{"internal", "alpha", "beta", "production", "qa-closed"}
	if len(rows) != len(wantOrder) {
		t.Fatalf("got %d rows (%v), want %d", len(rows), trackNames(rows), len(wantOrder))
	}
	for i, want := range wantOrder {
		if rows[i].Track != want {
			t.Errorf("row %d = %q, want %q (order: %v)", i, rows[i].Track, want, trackNames(rows))
		}
	}
}

func trackNames(rows []list.TrackRow) []string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Track
	}
	return names
}

// TestBuildRows_topRelease_isHighestVersionCode asserts that when a track
// carries several coexisting releases the row summarizes the one with the
// highest version code — the newest build on the track — including a
// draft when it is the highest (status disambiguates it for the reader).
func TestBuildRows_topRelease_isHighestVersionCode(t *testing.T) {
	api := []tracks.Track{
		{Track: "production", Releases: []tracks.Release{
			{Name: "142", Status: "completed", VersionCodes: []string{"142"}, UserFraction: 1.0},
			{Name: "143", Status: "inProgress", VersionCodes: []string{"143"}, UserFraction: 0.1},
			{Name: "150-draft", Status: "draft", VersionCodes: []string{"150"}},
		}},
	}
	row := rowByTrack(list.BuildRows(api))["production"]

	if !row.HasRelease {
		t.Fatal("production row HasRelease = false, want true")
	}
	if row.Release != "150-draft" {
		t.Errorf("top release name = %q, want 150-draft (highest version code)", row.Release)
	}
	if row.Status != "draft" {
		t.Errorf("top release status = %q, want draft", row.Status)
	}
	if strings.Join(row.VersionCodes, ",") != "150" {
		t.Errorf("top release versionCodes = %v, want [150]", row.VersionCodes)
	}
}

// TestBuildRows_topRelease_comparesMaxVersionCodePerRelease asserts that a
// release bundling several version codes is ranked by its highest code,
// so a [200,201] release outranks a [150] one.
func TestBuildRows_topRelease_comparesMaxVersionCodePerRelease(t *testing.T) {
	api := []tracks.Track{
		{Track: "beta", Releases: []tracks.Release{
			{Name: "single", Status: "completed", VersionCodes: []string{"150"}},
			{Name: "multi", Status: "inProgress", VersionCodes: []string{"200", "201"}, UserFraction: 0.2},
		}},
	}
	row := rowByTrack(list.BuildRows(api))["beta"]
	if row.Release != "multi" {
		t.Errorf("top release = %q, want multi (max code 201 > 150)", row.Release)
	}
}

// TestBuildRows_neverUsedStandardTrack_hasNoRelease asserts a standard
// track the API omitted is present but carries no release summary.
func TestBuildRows_neverUsedStandardTrack_hasNoRelease(t *testing.T) {
	row := rowByTrack(list.BuildRows(nil))["alpha"]
	if row.Track != "alpha" {
		t.Fatalf("alpha row missing: %+v", row)
	}
	if row.HasRelease {
		t.Errorf("never-used alpha HasRelease = true, want false")
	}
	if row.Release != "" || row.Status != "" || len(row.VersionCodes) != 0 {
		t.Errorf("never-used alpha row = %+v, want empty release fields", row)
	}
}

// TestDefaultColumns_documentedOrder pins the default table column order
// promised in --help and the issue's acceptance criteria.
func TestDefaultColumns_documentedOrder(t *testing.T) {
	want := []string{"track", "kind", "release", "status", "userFraction", "versionCodes"}
	cols, err := list.ResolveColumns("")
	if err != nil {
		t.Fatalf("ResolveColumns(\"\"): %v", err)
	}
	got := make([]string, len(cols))
	for i, c := range cols {
		got[i] = c.Key
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default columns = %v, want %v", got, want)
	}
}

// TestRenderTable_defaultColumns_summarizesEveryTrack asserts the table
// view renders one row per track (standard + custom), shows the kind, and
// formats userFraction as a percent for an active rollout while leaving
// it blank for completed and draft releases.
func TestRenderTable_defaultColumns_summarizesEveryTrack(t *testing.T) {
	api := []tracks.Track{
		{Track: "production", Releases: []tracks.Release{{Name: "143", Status: "inProgress", VersionCodes: []string{"143"}, UserFraction: 0.1}}},
		{Track: "beta", Releases: []tracks.Release{{Name: "140", Status: "completed", VersionCodes: []string{"140"}, UserFraction: 1.0}}},
		{Track: "qa-closed", Releases: []tracks.Release{{Name: "99", Status: "draft", VersionCodes: []string{"99"}}}},
	}
	defCols, _ := list.ResolveColumns("")
	p := list.Payload{Rows: list.BuildRows(api), Columns: defCols}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"TRACK", "KIND", "RELEASE", "STATUS", "USER_FRACTION", "VERSION_CODES"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing header %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"internal", "alpha", "beta", "production", "qa-closed", "standard", "custom"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	// inProgress 0.1 -> "10%"; completed 1.0 and draft -> blank.
	if !strings.Contains(out, "10%") {
		t.Errorf("table missing inProgress userFraction 10%%:\n%s", out)
	}
	if strings.Contains(out, "100%") {
		t.Errorf("table shows a percent for a completed release (should be blank):\n%s", out)
	}
}

// TestRenderMarkdown_isGFMTable_respectsColumnsOverride asserts the
// markdown view is a GFM table (header + `---` separator) and that
// --columns restricts the rendered columns.
func TestRenderMarkdown_isGFMTable_respectsColumnsOverride(t *testing.T) {
	api := []tracks.Track{
		{Track: "production", Releases: []tracks.Release{{Name: "143", Status: "inProgress", VersionCodes: []string{"143"}, UserFraction: 0.1}}},
	}
	cols, _ := list.ResolveColumns("track,kind")
	p := list.Payload{Rows: list.BuildRows(api), Columns: cols}

	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "production") || !strings.Contains(strings.ToUpper(out), "TRACK") {
		t.Errorf("markdown output = %q, want track column", out)
	}
	// The override dropped release/status/versionCodes columns.
	if strings.Contains(strings.ToLower(out), "version") || strings.Contains(strings.ToLower(out), "status") {
		t.Errorf("markdown output = %q, want only track,kind columns under override", out)
	}
}

// TestRun_unknownColumn_exit2 asserts an unknown --columns value is a CLI
// misuse caught before any HTTP call.
func TestRun_unknownColumn_exit2(t *testing.T) {
	rt := &listRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := list.Run(rc, list.Input{Package: "com.example.app", Columns: "track,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_unknownPackage_exit30WithHint asserts that an unknown package
// (edits.insert 404) maps to exit 30 with a hint pointing the operator at
// `gplay apps list`.
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
	// edits.insert failed, so no Edit was opened — nothing to discard.
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

// TestRun_tracksListError_discardsEditAndPropagates asserts the read-only
// Edit is discarded even when the listing itself fails after the Edit was
// opened, and the underlying status drives the exit code (5xx -> 40).
func TestRun_tracksListError_discardsEditAndPropagates(t *testing.T) {
	rt := &listRT{
		t:          t,
		editID:     "edit-err",
		tracksCode: 503,
		tracksResp: `{"error":{"code":503,"message":"Backend error"}}`,
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
		t.Errorf("Edit not discarded after a failed listing; calls = %v", rt.calls)
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

// The "unknown column key must not panic" guard these list commands used to
// carry is now structurally impossible: Payload.Columns is []output.Column,
// not bare string keys, so a Payload can only hold resolved columns with a
// non-nil Value. The exit-2 unknown-column path is covered by the command's
// --columns tests and by internal/output's ColumnSet.Resolve test.
