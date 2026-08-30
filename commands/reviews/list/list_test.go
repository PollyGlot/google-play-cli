package list

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// listRT is a RoundTripper covering both the OAuth2 /token exchange and the
// reviews.list calls a `reviews list` invocation makes. It serves the canned
// pages in order; reviewCode/errBody let a test force an error status on the
// reviews call (403, 404, ...).
type listRT struct {
	t          *testing.T
	pages      []string // reviews.list bodies, served in order
	reviewCode int      // 0 -> 200
	errBody    string

	mu        sync.Mutex
	calls     []string
	tokenHits int
	n         int
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

	if strings.HasSuffix(req.URL.Path, "/reviews") {
		if r.reviewCode != 0 {
			return jsonResp(r.reviewCode, r.errBody), nil
		}
		body := "{}"
		if r.n < len(r.pages) {
			body = r.pages[r.n]
		}
		r.n++
		return jsonResp(200, body), nil
	}
	r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	return nil, nil
}

func jsonResp(code int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     h,
	}
}

// signedSAJSON builds a syntactically valid service_account.json with a
// freshly generated RSA key so token.Source can sign a JWT in tests.
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

// newRC wires a RunContext whose HTTP client (token exchange + API calls)
// routes through rt, and returns the captured stdout and stderr buffers.
func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout, Stderr: &stderr}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout, &stderr
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

// twoReviewsBody is a single reviews.list page with a 5-star and a 1-star
// review, reused across the command tests.
const twoReviewsBody = `{"reviews":[
	{"reviewId":"r1","comments":[{"userComment":{"text":"Great app\nsecond line","starRating":5,"reviewerLanguage":"en","lastModified":{"seconds":"1700000000"}}}]},
	{"reviewId":"r2","comments":[{"userComment":{"text":"Bad","starRating":1,"reviewerLanguage":"fr-FR","lastModified":{"seconds":"1700000100"}}}]}
]}`

// ids extracts the reviewIds of a payload's surviving reviews, in order.
func ids(p Payload) []string {
	out := make([]string, len(p.Reviews))
	for i, r := range p.Reviews {
		out[i] = r.ReviewID
	}
	return out
}

func TestRun_starsFilter_keepsOnlyMatching(t *testing.T) {
	// twoReviewsBody: r1=5★, r2=1★.
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Stars: "1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := r.(Payload)
	if got := ids(p); len(got) != 1 || got[0] != "r2" {
		t.Errorf("--stars 1 should keep only r2, got %v", got)
	}
}

const fourReviewsBody = `{"reviews":[
	{"reviewId":"r1","comments":[{"userComment":{"starRating":5,"reviewerLanguage":"en"}}]},
	{"reviewId":"r2","comments":[{"userComment":{"starRating":1,"reviewerLanguage":"en"}}]},
	{"reviewId":"r3","comments":[{"userComment":{"starRating":5,"reviewerLanguage":"en"}}]},
	{"reviewId":"r4","comments":[{"userComment":{"starRating":1,"reviewerLanguage":"en"}}]}
]}`

func TestRun_limitCapsAfterFilter(t *testing.T) {
	// stars=1 keeps r2 and r4 (in order); limit=1 then caps to r2: proving
	// the cap is applied AFTER the filter, not against the raw page.
	rt := &listRT{t: t, pages: []string{fourReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Stars: "1", Limit: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ids(r.(Payload)); len(got) != 1 || got[0] != "r2" {
		t.Errorf("--stars 1 --limit 1 should yield [r2], got %v", got)
	}
}

func TestRun_limitZeroMeansNoCap(t *testing.T) {
	rt := &listRT{t: t, pages: []string{fourReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Limit: 0})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ids(r.(Payload)); len(got) != 4 {
		t.Errorf("--limit 0 should keep all 4 reviews, got %v", got)
	}
}

// TestRun_limitTruncation_warnsOnStderr covers the reviews half of PRD #446 /
// #451. Here the whole set is already in hand (reviews.List exhausts the token
// before filtering), so "truncated" means the --limit dropped rows we were
// holding: silence there would let a capped list read as the whole match.
//
// Stdout is untouched by the warning (ADR-0003): the payload still carries
// exactly the capped rows and nothing about the warning.
func TestRun_limitTruncation_warnsOnStderr(t *testing.T) {
	rt := &listRT{t: t, pages: []string{fourReviewsBody}}
	rc, stdout, stderr := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Limit: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ids(r.(Payload)); len(got) != 2 {
		t.Fatalf("--limit 2 should yield 2 reviews, got %v", got)
	}
	got := stderr.String()
	for _, want := range []string{"warning:", "truncated", "--limit"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q, want it to contain %q", got, want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (Run returns a payload; the kernel renders it)", stdout.String())
	}
}

// TestRun_limitNotReached_doesNotWarn is the other half: a --limit larger than
// the result set is not a truncation, and warning there would be noise.
func TestRun_limitNotReached_doesNotWarn(t *testing.T) {
	rt := &listRT{t: t, pages: []string{fourReviewsBody}}
	rc, _, stderr := newRC(t, rt)

	if _, err := Run(rc, Input{Package: "com.example.app", Limit: 10}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(stderr.String(), "truncated") {
		t.Errorf("stderr = %q, want no truncation warning (--limit 10 over 4 reviews)", stderr.String())
	}
}

func TestRun_negativeLimit_exit2(t *testing.T) {
	rt := &listRT{t: t, pages: []string{fourReviewsBody}}
	rc, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app", Limit: -3})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("--limit -3: exit code = %d, want 2", code)
	}
}

func TestPayload_Table_defaultColumns_summaryFirstLine(t *testing.T) {
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("Render table: %v", err)
	}
	out := buf.String()

	for _, h := range []string{"DATE", "STARS", "LOCALE", "REVIEW_ID", "SUMMARY"} {
		if !strings.Contains(out, h) {
			t.Errorf("table header missing %q in:\n%s", h, out)
		}
	}
	// Both reviews appear, with locale and id.
	if !strings.Contains(out, "r1") || !strings.Contains(out, "r2") || !strings.Contains(out, "fr-FR") {
		t.Errorf("table missing expected rows:\n%s", out)
	}
	// Summary is the FIRST line of the comment, not the whole body.
	if !strings.Contains(out, "Great app") {
		t.Errorf("table should show first line 'Great app':\n%s", out)
	}
	if strings.Contains(out, "second line") {
		t.Errorf("summary must be the first line only, not the full body:\n%s", out)
	}
}

func TestRun_columnsOverride_dropsOthers(t *testing.T) {
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Columns: "stars,reviewId"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "STARS") || !strings.Contains(out, "REVIEW_ID") {
		t.Errorf("override should keep stars + reviewId:\n%s", out)
	}
	for _, dropped := range []string{"DATE", "LOCALE", "SUMMARY"} {
		if strings.Contains(out, dropped) {
			t.Errorf("override should drop %q:\n%s", dropped, out)
		}
	}
}

func TestRun_unknownColumn_exit2(t *testing.T) {
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app", Columns: "stars,bogus"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("unknown column: exit code = %d, want 2", code)
	}
}

func TestPayload_Markdown_isAGFMTable(t *testing.T) {
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatMarkdown, r.Renderers()); err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| DATE | STARS | LOCALE | REVIEW_ID | SUMMARY |") {
		t.Errorf("markdown header row missing:\n%s", out)
	}
	if !strings.Contains(out, "| --- |") {
		t.Errorf("markdown separator row missing:\n%s", out)
	}
	if !strings.Contains(out, "r1") || !strings.Contains(out, "r2") {
		t.Errorf("markdown rows missing:\n%s", out)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"first line only", "Great app\nsecond line", "Great app"},
		{"trims spaces", "   spaced   ", "spaced"},
		{"empty", "", ""},
		{"truncates long line", strings.Repeat("a", 70), strings.Repeat("a", 60) + "…"},
		{"skips leading blank lines", "\n\n  \nReal content here", "Real content here"},
		{"collapses interior tab", "price:\t5 stars", "price: 5 stars"},
		{"collapses tab run on first line", "a\t\tb", "a b"},
		{"strips trailing carriage return", "Great app\r\nsecond line", "Great app"},
		{"all blank yields empty", "\n  \n\t\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary(tc.in); got != tc.want {
				t.Errorf("summary(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPayload_JSON_reflectsFilteredSet_verbatim(t *testing.T) {
	// r1=5★, r2=1★; --stars 1 keeps only r2.
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app", Stars: "1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("Render JSON: %v", err)
	}

	var env struct {
		Reviews []struct {
			ReviewID string `json:"reviewId"`
			Comments []struct {
				UserComment struct {
					StarRating       int    `json:"starRating"`
					ReviewerLanguage string `json:"reviewerLanguage"`
				} `json:"userComment"`
			} `json:"comments"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not the {\"reviews\":[...]} envelope: %v\n%s", err, buf.String())
	}
	if len(env.Reviews) != 1 {
		t.Fatalf("JSON should reflect the filtered set (1 review), got %d", len(env.Reviews))
	}
	got := env.Reviews[0]
	if got.ReviewID != "r2" {
		t.Errorf("survivor reviewId = %q, want r2", got.ReviewID)
	}
	// The review object is passed through verbatim: nested fields intact.
	if got.Comments[0].UserComment.StarRating != 1 || got.Comments[0].UserComment.ReviewerLanguage != "fr-FR" {
		t.Errorf("verbatim review fields lost: %+v", got)
	}
}

func TestRun_forbidden_exit11_withReplyHint(t *testing.T) {
	rt := &listRT{t: t, reviewCode: 403, errBody: `{"error":{"code":403,"message":"caller lacks permission"}}`}
	rc, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("403 should map to exit 11, got %d (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "Reply to reviews") {
		t.Errorf("403 error should hint at the 'Reply to reviews' permission; got %q", err.Error())
	}
}

// timeoutRT serves the /token exchange instantly but blocks the reviews.list
// call until the request context is canceled: standing in for a hung upstream
// connection. A well-behaved transport observes ctx cancellation, which is how
// the kernel-applied deadline (the global --timeout / 60s default) interrupts
// the request.
type timeoutRT struct{}

func (timeoutRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestRun_timeout_mapsToExit50 drives `reviews list` against a transport whose
// API call hangs, with a tight global --timeout. The kernel-applied deadline
// must interrupt the request, and the transport-level failure must surface as
// exit 50 (network): bounded well under the would-be hang, not stalling until
// a runner-level kill.
func TestRun_timeout_mapsToExit50(t *testing.T) {
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: timeoutRT{}})
	boot := kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON, Timeout: 150 * time.Millisecond})
	rc.Account = sa

	start := time.Now()
	_, err = Run(rc, Input{Package: "com.example.app"})
	elapsed := time.Since(start)

	if code := exitCodeOf(t, err); code != 50 {
		t.Fatalf("hung request should map to exit 50 (network), got %d (err: %v)", code, err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("request was not bounded by the 150ms --timeout; took %v", elapsed)
	}
}

// retryRT serves the /token exchange, then fails the first reviews.list call
// with a 500 and serves a valid page on the second: exercising the kernel's
// --retry wiring end-to-end through a real command.
type retryRT struct {
	body           string
	reviewAttempts int
}

func (r *retryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.reviewAttempts++
	if r.reviewAttempts == 1 {
		return jsonResp(500, `{"error":{"code":500,"message":"backend hiccup"}}`), nil
	}
	return jsonResp(200, r.body), nil
}

// TestRun_retry_recoversFrom5xx drives `reviews list` with --retry 1 against a
// transport whose first API call 500s and second succeeds: the command must
// recover and return the payload (proving Inputs.Retry layers the retry
// middleware onto the authed client).
func TestRun_retry_recoversFrom5xx(t *testing.T) {
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	rt := &retryRT{body: twoReviewsBody}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	boot := kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: output.FormatJSON, Retry: 1})
	rc.Account = sa

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run with --retry should recover from a 500, got %v", err)
	}
	if rt.reviewAttempts != 2 {
		t.Errorf("reviews.list attempts = %d, want 2 (1 retry after the 500)", rt.reviewAttempts)
	}
	if got := ids(r.(Payload)); len(got) != 2 {
		t.Errorf("recovered payload should hold both reviews, got %v", got)
	}
}

func TestRun_unknownPackage_exit30(t *testing.T) {
	rt := &listRT{t: t, reviewCode: 404, errBody: `{"error":{"code":404,"message":"not found"}}`}
	rc, _, _ := newRC(t, rt)

	_, err := Run(rc, Input{Package: "com.example.nope"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("404 should map to exit 30, got %d (err: %v)", code, err)
	}
}

// TestRun_hostileReviewText_tableSanitized_jsonByteFaithful is the #213
// demonstration case: a user-generated review whose text carries ANSI escape
// sequences and control bytes must be NEUTRALIZED in table/markdown output (no
// escape can reach the terminal), yet pass through BYTE-FAITHFULLY in JSON so
// machine consumers see the data exactly as the API returned it (ADR-0003).
func TestRun_hostileReviewText_tableSanitized_jsonByteFaithful(t *testing.T) {
	// Real ESC/BEL/control bytes plus legitimate multi-byte content.
	const hostileText = "\x1b[31mRED\x1b[0m\x07 visit evil.example \x1b]0;pwn\x07 café 日本 🎉"
	raw, err := json.Marshal(map[string]any{
		"reviews": []any{map[string]any{
			"reviewId": "r1",
			"comments": []any{map[string]any{
				"userComment": map[string]any{
					"text":             hostileText,
					"starRating":       5,
					"reviewerLanguage": "en",
					"lastModified":     map[string]any{"seconds": "1700000000"},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal hostile body: %v", err)
	}
	rt := &listRT{t: t, pages: []string{string(raw)}}
	rc, _, _ := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Table: no escape/BEL byte survives; legitimate content remains.
	var tbuf bytes.Buffer
	if err := output.Render(&tbuf, output.FormatTable, r.Renderers()); err != nil {
		t.Fatalf("Render table: %v", err)
	}
	tab := tbuf.String()
	if strings.ContainsRune(tab, '\x1b') || strings.ContainsRune(tab, '\x07') {
		t.Errorf("table output still carries an escape/BEL byte:\n%q", tab)
	}
	for _, want := range []string{"RED", "café", "日本", "🎉"} {
		if !strings.Contains(tab, want) {
			t.Errorf("table output lost legitimate content %q:\n%q", want, tab)
		}
	}

	// JSON: byte-faithful: decoding the output yields the original text,
	// control bytes and all (no sanitization on the machine path).
	var jbuf bytes.Buffer
	if err := output.Render(&jbuf, output.FormatJSON, r.Renderers()); err != nil {
		t.Fatalf("Render JSON: %v", err)
	}
	var env struct {
		Reviews []struct {
			Comments []struct {
				UserComment struct {
					Text string `json:"text"`
				} `json:"userComment"`
			} `json:"comments"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(jbuf.Bytes(), &env); err != nil {
		t.Fatalf("JSON output not decodable: %v\n%s", err, jbuf.String())
	}
	if len(env.Reviews) != 1 || len(env.Reviews[0].Comments) != 1 {
		t.Fatalf("unexpected JSON shape: %s", jbuf.String())
	}
	if got := env.Reviews[0].Comments[0].UserComment.Text; got != hostileText {
		t.Errorf("JSON text not byte-faithful:\n got  %q\n want %q", got, hostileText)
	}
}

func TestRun_emptyResult_isNotAnError_butStillWarns(t *testing.T) {
	rt := &listRT{t: t, pages: []string{`{"reviews":[]}`}}
	rc, _, stderr := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("empty result must not be an error, got %v", err)
	}
	if got := ids(r.(Payload)); len(got) != 0 {
		t.Errorf("expected zero reviews, got %v", got)
	}
	if !strings.Contains(stderr.String(), "WARN:") {
		t.Errorf("empty result must still print the 7-day WARN; stderr=%q", stderr.String())
	}
}

func TestRun_invalidStars_exit2(t *testing.T) {
	for _, spec := range []string{"0", "6", "5-1", "abc"} {
		t.Run(spec, func(t *testing.T) {
			rt := &listRT{t: t, pages: []string{twoReviewsBody}}
			rc, _, _ := newRC(t, rt)

			_, err := Run(rc, Input{Package: "com.example.app", Stars: spec})
			if code := exitCodeOf(t, err); code != 2 {
				t.Errorf("--stars %q: exit code = %d, want 2", spec, code)
			}
			// A bad selector is rejected before any network call.
			if rt.tokenHits != 0 || len(rt.calls) != 0 {
				t.Errorf("--stars %q reached the network; calls=%v", spec, rt.calls)
			}
		})
	}
}

func TestRun_happyPath_warnsAndReturnsPayload(t *testing.T) {
	rt := &listRT{t: t, pages: []string{twoReviewsBody}}
	rc, _, stderr := newRC(t, rt)

	r, err := Run(rc, Input{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r == nil {
		t.Fatal("Run returned nil Renderable on the happy path")
	}

	if rt.tokenHits == 0 {
		t.Errorf("no /token exchange happened; calls=%v", rt.calls)
	}
	wantCall := "GET /androidpublisher/v3/applications/com.example.app/reviews"
	found := false
	for _, c := range rt.calls {
		if c == wantCall {
			found = true
		}
	}
	if !found {
		t.Errorf("reviews.list was not called; calls=%v", rt.calls)
	}

	// The 7-day window warning is always printed to stderr on success.
	// Assert against the constant so rewording it cannot silently weaken
	// this check, while a genuinely missing warning still fails.
	if !strings.Contains(stderr.String(), sevenDayWarning) {
		t.Errorf("stderr = %q, want the 7-day window warning %q", stderr.String(), sevenDayWarning)
	}
}
