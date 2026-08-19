// Package view_test exercises `gplay tracks view` at the kernel
// level: a RunContext built by hand, a RoundTripper injected via the
// oauth2.HTTPClient context key, and Run invoked directly. Mirrors the
// `tracks list` harness, but routes tracks.get (the single-track deep
// read, GET .../tracks/<name>) instead of tracks.list. The transport
// FAILS on any PUT or :commit: a read-only track view opens, reads,
// and discards the Edit, never writes or commits it.
package view_test

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

	"github.com/PollyGlot/google-play-cli/commands/tracks/view"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// statusRT terminates the OAuth2 /token exchange and routes the
// read-only single-track sequence: edits.insert, tracks.get
// (GET .../tracks/<name>), edits.delete. It deliberately has NO PUT or
// :commit branch: reaching one means the command tried to mutate or
// commit, which a read-only status view must never do, so the transport
// fails the test.
type statusRT struct {
	t          *testing.T
	editID     string
	trackResp  string
	insertCode int // 0 -> 200
	trackCode  int // 0 -> 200
	insertBody string

	mu        sync.Mutex
	calls     []string
	tokenHits int
}

func (r *statusRT) RoundTrip(req *http.Request) (*http.Response, error) {
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
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tracks/"):
		code := r.trackCode
		if code == 0 {
			code = 200
		}
		return jsonResp(code, r.trackResp), nil
	}
	r.t.Fatalf("unexpected request (read-only status must not write/commit): %s %s", req.Method, req.URL)
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

// TestRun_statusOfTrack_happyPath is the tracer bullet: /token precedes
// edits.insert, then tracks.get (GET .../tracks/production, the
// single-track deep read), then the Edit is DISCARDED (never committed).
// --output json is the raw tracks.get payload (ADR-0003 pass-through).
func TestRun_statusOfTrack_happyPath(t *testing.T) {
	raw := `{"track":"production","releases":[` +
		`{"name":"142","status":"completed","versionCodes":["142"],"userFraction":1.0},` +
		`{"name":"143","status":"inProgress","versionCodes":["143"],"userFraction":0.1}` +
		`]}`
	rt := &statusRT{t: t, editID: "edit-status", trackResp: raw}
	rc, _ := newRC(t, rt)

	r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
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
		"GET /androidpublisher/v3/applications/com.example.app/edits/edit-status/tracks/production",
		"DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-status",
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

// TestRenderTable_halted_isVisuallyDistinct is the headline feature: a
// halted release must jump out at a glance. The status column renders
// `halted` uppercased and prefixed with `!` (-> !HALTED), so an operator
// scanning the table cannot confuse it with a healthy inProgress rollout,
// which is left in its plain API form.
func TestRenderTable_halted_isVisuallyDistinct(t *testing.T) {
	cols, _ := view.ResolveColumns("status")
	p := view.Payload{
		Releases: []tracks.Release{
			{Name: "143", Status: "halted", VersionCodes: []string{"143"}, UserFraction: 0.1},
			{Name: "144", Status: "inProgress", VersionCodes: []string{"144"}, UserFraction: 0.2},
		},
		Columns: cols,
	}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "!HALTED") {
		t.Errorf("table missing halted marker !HALTED:\n%s", out)
	}
	// The healthy rollout stays in its plain API form: it must NOT pick
	// up the halted marker, otherwise the marker stops meaning "halted".
	if !strings.Contains(out, "inProgress") {
		t.Errorf("table missing plain inProgress status:\n%s", out)
	}
	if strings.Contains(out, "!INPROGRESS") {
		t.Errorf("inProgress wrongly marked as halted:\n%s", out)
	}
}

// lineContaining returns the first line of out that contains needle, or
// "" if none does. Used to assert one release's row in a rendered table.
func lineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// TestRenderTable_listsEveryCoexistingRelease asserts the deep view: every
// release coexisting on the track: draft, inProgress, halted, completed
// alike: gets its own row under the default columns, and the release-notes
// locale count is rendered as the count of localized entries.
func TestRenderTable_listsEveryCoexistingRelease(t *testing.T) {
	p := view.Payload{
		Releases: []tracks.Release{
			{Name: "150-draft", Status: "draft", VersionCodes: []string{"150"}},
			{Name: "143", Status: "inProgress", VersionCodes: []string{"143"}, UserFraction: 0.1},
			{Name: "142", Status: "halted", VersionCodes: []string{"142"}, UserFraction: 0.05},
			{Name: "140-live", Status: "completed", VersionCodes: []string{"140"}, UserFraction: 1.0, ReleaseNotes: []tracks.LocalizedText{
				{Language: "en-US", Text: "Bug fixes"},
				{Language: "fr-FR", Text: "Corrections"},
			}},
		},
		Columns: mustDefaultColumns(t),
	}

	var buf bytes.Buffer
	if err := p.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"NAME", "STATUS", "USER_FRACTION", "VERSION_CODES", "NOTES_LOCALES"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing header %q:\n%s", want, out)
		}
	}
	// All four coexisting releases get a row, regardless of status.
	for _, name := range []string{"150-draft", "143", "142", "140-live"} {
		if !strings.Contains(out, name) {
			t.Errorf("table missing release %q:\n%s", name, out)
		}
	}
	// The completed release carries two release-notes locales -> "2" in
	// its NOTES_LOCALES cell (the last field of its row).
	row := lineContaining(out, "140-live")
	fields := strings.Fields(row)
	if len(fields) == 0 || fields[len(fields)-1] != "2" {
		t.Errorf("completed release notes-locale count = %v, want last field 2:\nrow=%q", fields, row)
	}
}

// TestRun_derivesTrackKind asserts the command labels the track standard
// vs custom from its name: the four well-known tracks Google Play
// provisions for every app are "standard"; anything else is a "custom"
// closed track. The kind is gplay-derived (the API has no such field), so
// it appears in the human table view, alongside the track name.
func TestRun_derivesTrackKind(t *testing.T) {
	cases := map[string]string{
		"internal":   "standard",
		"alpha":      "standard",
		"beta":       "standard",
		"production": "standard",
		"qa-closed":  "custom",
	}
	for track, wantKind := range cases {
		t.Run(track, func(t *testing.T) {
			raw := `{"track":"` + track + `","releases":[{"name":"1","status":"completed","versionCodes":["1"]}]}`
			rt := &statusRT{t: t, editID: "edit-kind", trackResp: raw}
			rc, _ := newRC(t, rt)

			r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: track})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			var buf bytes.Buffer
			if err := r.Renderers().Table(&buf); err != nil {
				t.Fatalf("Table render: %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, track) {
				t.Errorf("table missing track name %q:\n%s", track, out)
			}
			if !strings.Contains(out, wantKind) {
				t.Errorf("table missing kind %q for track %q:\n%s", wantKind, track, out)
			}
		})
	}
}

// TestDefaultColumns_documentedOrder pins the default table column order
// promised in --help and the issue's acceptance criteria.
func TestDefaultColumns_documentedOrder(t *testing.T) {
	want := []string{"name", "status", "userFraction", "versionCodes", "notes"}
	got := make([]string, 0, len(want))
	for _, c := range mustDefaultColumns(t) {
		got = append(got, c.Key)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default columns = %v, want %v", got, want)
	}
}

// mustDefaultColumns resolves the default column set for a Payload built
// directly in a render test, failing the test if resolution errors (it
// cannot, for the empty spec: this just keeps the call sites terse).
func mustDefaultColumns(t *testing.T) []output.Column[tracks.Release] {
	t.Helper()
	cols, err := view.ResolveColumns("")
	if err != nil {
		t.Fatalf("ResolveColumns(\"\"): %v", err)
	}
	return cols
}

// TestRun_columnsOverride_restrictsColumns asserts --columns narrows the
// rendered table to exactly the requested columns, in order.
func TestRun_columnsOverride_restrictsColumns(t *testing.T) {
	raw := `{"track":"production","releases":[{"name":"143","status":"inProgress","versionCodes":["143"],"userFraction":0.1}]}`
	rt := &statusRT{t: t, editID: "edit-cols", trackResp: raw}
	rc, _ := newRC(t, rt)

	r, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production", Columns: "name,status"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Renderers().Table(&buf); err != nil {
		t.Fatalf("Table render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing requested column %q:\n%s", want, out)
		}
	}
	for _, drop := range []string{"USER_FRACTION", "VERSION_CODES", "NOTES_LOCALES"} {
		if strings.Contains(out, drop) {
			t.Errorf("table shows column %q dropped by --columns override:\n%s", drop, out)
		}
	}
}

// TestRun_unknownColumn_exit2 asserts an unknown --columns value is a CLI
// misuse caught before any HTTP call.
func TestRun_unknownColumn_exit2(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production", Columns: "name,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRenderMarkdown_isGFMTable_marksHalted asserts the markdown view is a
// GFM table (header + `---` separator) over the same releases, and that
// the halted marker carries into it: markdown is a human-facing view, so
// a halted rollout must stand out there too.
func TestRenderMarkdown_isGFMTable_marksHalted(t *testing.T) {
	p := view.Payload{
		Track: "production",
		Kind:  "standard",
		Releases: []tracks.Release{
			{Name: "143", Status: "halted", VersionCodes: []string{"143"}, UserFraction: 0.1},
		},
		Columns: mustDefaultColumns(t),
	}

	var buf bytes.Buffer
	if err := p.Renderers().Markdown(&buf); err != nil {
		t.Fatalf("Markdown render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "---") {
		t.Errorf("markdown output = %q, want a GFM `---` separator row", out)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "143") {
		t.Errorf("markdown output = %q, want the release row", out)
	}
	if !strings.Contains(out, "!HALTED") {
		t.Errorf("markdown output = %q, want the halted marker !HALTED", out)
	}
	// Markdown carries the same track/kind context as the table view, so
	// a pasted report stands on its own without the command line.
	if !strings.Contains(out, "Track: production (standard)") {
		t.Errorf("markdown output = %q, want the track/kind context line", out)
	}
}

// TestRenderJSON_emptyRaw_errors asserts the JSON view refuses to emit a
// Payload with no captured raw body rather than silently writing zero
// bytes (exit 0). Every Payload field is json:"-", so an empty Raw could
// only ever produce a bare body and break the ADR-0003 pass-through
// contract: defensive parity with the tracks.list sibling.
func TestRenderJSON_emptyRaw_errors(t *testing.T) {
	var buf bytes.Buffer
	if err := (view.Payload{}).Renderers().JSON(&buf); err == nil {
		t.Fatalf("JSON render of empty Raw = nil error, want a non-nil error")
	}
}

// TestRun_unknownTrack_exit30WithHint asserts that an unknown track
// (tracks.get 404) maps to exit 30 with a hint pointing the operator at
// `gplay tracks list`, and that the read-only Edit is still discarded.
func TestRun_unknownTrack_exit30WithHint(t *testing.T) {
	rt := &statusRT{
		t:         t,
		editID:    "edit-404",
		trackCode: 404,
		trackResp: `{"error":{"code":404,"message":"Track not found.","errors":[{"reason":"trackNotFound"}]}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "nope"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "tracks list") {
		t.Errorf("error %q, want a hint mentioning `gplay tracks list`", err.Error())
	}
	sawDelete := false
	for _, c := range rt.calls {
		if strings.HasPrefix(c, "DELETE ") && strings.Contains(c, "/edits/edit-404") {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("Edit not discarded after an unknown-track read; calls = %v", rt.calls)
	}
}

// TestRun_unknownPackage_exit30WithHint asserts that an unknown package
// (edits.insert 404, raised before the read closure runs) maps to exit 30
// with a hint pointing the operator at `gplay apps list`: mirroring the
// sibling `gplay tracks list`. The closure never runs, so the track is
// never read; the package miss is caught at insert, distinct from the
// tracks.get 404 (unknown track) that points at `gplay tracks list`.
func TestRun_unknownPackage_exit30WithHint(t *testing.T) {
	rt := &statusRT{
		t:          t,
		insertCode: 404,
		insertBody: `{"error":{"code":404,"message":"The requested package does not exist."}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("ExitCode() = %d, want 30", code)
	}
	if !strings.Contains(err.Error(), "apps list") {
		t.Errorf("error %q, want a hint mentioning `gplay apps list`", err.Error())
	}
	for _, c := range rt.calls {
		if strings.Contains(c, "/tracks/") {
			t.Errorf("track was read despite an insert (package) failure; calls = %v", rt.calls)
		}
	}
}

// TestRun_forbidden_exit11WithHint asserts that a 403 (service account not
// invited on the app) maps to exit 11 with the standard grant-access hint.
func TestRun_forbidden_exit11WithHint(t *testing.T) {
	rt := &statusRT{
		t:          t,
		insertCode: 403,
		insertBody: `{"error":{"code":403,"message":"The caller does not have permission"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Errorf("ExitCode() = %d, want 11", code)
	}
	if !strings.Contains(err.Error(), "API access") {
		t.Errorf("error %q, want a grant-access hint mentioning Play Console → Setup → API access", err.Error())
	}
}

// TestRun_tracksGetError_discardsEditAndPropagates asserts the read-only
// Edit is discarded even when the read itself fails after the Edit was
// opened, and the underlying status drives the exit code (5xx -> 40).
func TestRun_tracksGetError_discardsEditAndPropagates(t *testing.T) {
	rt := &statusRT{
		t:         t,
		editID:    "edit-err",
		trackCode: 503,
		trackResp: `{"error":{"code":503,"message":"Backend error"}}`,
	}
	rc, _ := newRC(t, rt)

	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
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
		t.Errorf("Edit not discarded after a failed read; calls = %v", rt.calls)
	}
}

// TestRun_missingTrack_exit2 asserts a missing --track is a usage error
// caught before any HTTP call.
func TestRun_missingTrack_exit2(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := view.Run(rc, view.Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_missingPackage_exit2 asserts a missing package (no --package and
// no pin) is a usage error before any HTTP call.
func TestRun_missingPackage_exit2(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := view.Run(rc, view.Input{Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_whitespaceTrack_exit2 asserts a --track that is only whitespace
// is treated as missing: a usage error (exit 2) caught before any HTTP
// call, not a track named "   " sent to the API. Mirrors the empty-track
// guard: leading/trailing whitespace from a shell or CI variable must be
// trimmed at the input boundary.
func TestRun_whitespaceTrack_exit2(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "   "})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_whitespacePackage_exit2 asserts a --package that is only
// whitespace is treated as missing (exit 2) before any HTTP call.
func TestRun_whitespacePackage_exit2(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	_, err := view.Run(rc, view.Input{Package: "   ", Track: "production"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("ExitCode() = %d, want 2", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before usage error, saw: %v", rt.calls)
	}
}

// TestRun_noAccount_exit10 asserts that with no resolved Account the
// command fails auth (exit 10) before any HTTP call.
func TestRun_noAccount_exit10(t *testing.T) {
	rt := &statusRT{t: t}
	rc, _ := newRC(t, rt)
	rc.Account = nil
	_, err := view.Run(rc, view.Input{Package: "com.example.app", Track: "production"})
	if code := exitCodeOf(t, err); code != 10 {
		t.Errorf("ExitCode() = %d, want 10", code)
	}
	if len(rt.calls) != 0 {
		t.Errorf("expected zero HTTP calls before auth error, saw: %v", rt.calls)
	}
}
