package reply

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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// replyRT covers the OAuth2 /token exchange and the reviews.reply POSTs an
// invocation makes. status maps a reviewId to a forced HTTP status (absent →
// 200); posted records the reviewIds posted, in order, so a test can assert
// the wire calls and that a per-line failure did not abort the batch.
type replyRT struct {
	t      *testing.T
	status map[string]int

	mu        sync.Mutex
	posted    []string
	tokenHits int
}

func (r *replyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.tokenHits++
		return jsonResp(200, `{"access_token":"abc.def.ghi","token_type":"Bearer","expires_in":3600}`), nil
	}
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, ":reply") {
		r.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		return nil, nil
	}
	id := reviewIDFromPath(req.URL.Path)
	r.posted = append(r.posted, id)
	code := 200
	if c, ok := r.status[id]; ok {
		code = c
	}
	if code != 200 {
		return jsonResp(code, fmt.Sprintf(`{"error":{"code":%d,"message":"nope"}}`, code)), nil
	}
	return jsonResp(200, fmt.Sprintf(`{"result":{"replyText":"echo for %s"}}`, id)), nil
}

// reviewIDFromPath recovers the reviewId from a .../reviews/<id>:reply path.
func reviewIDFromPath(p string) string {
	const marker = "/reviews/"
	i := strings.Index(p, marker)
	if i < 0 {
		return ""
	}
	seg := strings.TrimSuffix(p[i+len(marker):], ":reply")
	if dec, err := url.PathUnescape(seg); err == nil {
		return dec
	}
	return seg
}

func jsonResp(code int, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: h}
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

// newRC wires a RunContext whose HTTP client routes through rt, in the given
// output Format, with stdin as the batch source. Returns the captured stdout
// and stderr buffers.
func newRC(t *testing.T, rt http.RoundTripper, format output.Format, stdin io.Reader) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	boot := kernel.Boot{Stdout: &stdout, Stderr: &stderr, Stdin: stdin}
	rc := kernel.NewForTest(ctx, boot, kernel.Inputs{Format: format})
	rc.Account = sa
	return rc, &stdout, &stderr
}

// writeTSV writes content to a temp file and returns its path.
func writeTSV(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "replies.tsv")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write tsv: %v", err)
	}
	return p
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

func TestRun_single_postsAndEchoesJSON(t *testing.T) {
	rt := &replyRT{t: t}
	rc, stdout, stderr := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1", Reply: "thanks!"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.posted) != 1 || rt.posted[0] != "r1" {
		t.Fatalf("posted = %v, want [r1]", rt.posted)
	}
	// The success line goes to stderr (stdout is reserved for data).
	if !strings.Contains(stderr.String(), "Reply posted on r1") {
		t.Errorf("stderr should confirm the post; got %q", stderr.String())
	}
	// --output json echoes the API response verbatim on stdout (ADR-0003).
	if !strings.Contains(stdout.String(), "echo for r1") {
		t.Errorf("stdout should carry the API response; got %q", stdout.String())
	}
}

func TestRun_single_nonJSON_stdoutEmpty(t *testing.T) {
	rt := &replyRT{t: t}
	rc, stdout, stderr := newRC(t, rt, output.FormatTable, nil)

	if err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1", Reply: "hi"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("non-json single mode should leave stdout empty; got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Reply posted on r1") {
		t.Errorf("confirmation should still be on stderr; got %q", stderr.String())
	}
}

func TestRun_reviewIDAndBatch_exit2(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, _ := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1", Reply: "hi", BatchSet: true, Batch: "f.tsv"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("review-id + batch: exit = %d, want 2", code)
	}
	if rt.tokenHits != 0 || len(rt.posted) != 0 {
		t.Errorf("mutually-exclusive misuse must not reach the network; posted=%v", rt.posted)
	}
}

func TestRun_noMode_exit2(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, _ := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("no mode: exit = %d, want 2", code)
	}
}

func TestRun_reviewIDWithoutReply_exit2(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, _ := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1"})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("--review-id without --reply: exit = %d, want 2", code)
	}
}

func TestRun_single_forbidden_exit11_withHint(t *testing.T) {
	rt := &replyRT{t: t, status: map[string]int{"r1": 403}}
	rc, _, _ := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1", Reply: "hi"})
	if code := exitCodeOf(t, err); code != 11 {
		t.Fatalf("403 should map to exit 11, got %d (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "Reply to reviews") {
		t.Errorf("403 should carry the shared 'Reply to reviews' hint; got %q", err.Error())
	}
}

func TestRun_single_unknownReview_exit30(t *testing.T) {
	rt := &replyRT{t: t, status: map[string]int{"bad": 404}}
	rc, _, _ := newRC(t, rt, output.FormatJSON, nil)

	err := Run(rc, Input{Package: "com.example.app", ReviewID: "bad", Reply: "hi"})
	if code := exitCodeOf(t, err); code != 30 {
		t.Fatalf("404 should map to exit 30, got %d (err: %v)", code, err)
	}
	// The 404 must name the review, not point at `gplay apps list`.
	if !strings.Contains(err.Error(), "review") || strings.Contains(err.Error(), "gplay apps list") {
		t.Errorf("reply 404 hint should name the review; got %q", err.Error())
	}
}

func TestRun_single_dryRun_noNetwork(t *testing.T) {
	rt := &replyRT{t: t}
	rc, stdout, stderr := newRC(t, rt, output.FormatJSON, nil)

	if err := Run(rc, Input{Package: "com.example.app", ReviewID: "r1", Reply: "hi", DryRun: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt.tokenHits != 0 || len(rt.posted) != 0 {
		t.Errorf("--dry-run must not touch the network; posted=%v tokenHits=%d", rt.posted, rt.tokenHits)
	}
	if !strings.Contains(stderr.String(), "DRY-RUN") || !strings.Contains(stderr.String(), "r1") {
		t.Errorf("--dry-run should preview the planned reply on stderr; got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("--dry-run stdout should be empty; got %q", stdout.String())
	}
}

func TestRun_batch_file_postsEachInOrder(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, stderr := newRC(t, rt, output.FormatTable, nil)

	path := writeTSV(t, "r1\tthanks\nr2\tcheers\n")
	if err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path}); err != nil {
		t.Fatalf("all-success batch should exit 0; got %v", err)
	}
	if got := rt.posted; len(got) != 2 || got[0] != "r1" || got[1] != "r2" {
		t.Fatalf("posted = %v, want [r1 r2] in order", got)
	}
	for _, id := range []string{"r1", "r2"} {
		if !strings.Contains(stderr.String(), "OK "+id) {
			t.Errorf("stderr should report OK %s; got %q", id, stderr.String())
		}
	}
}

func TestRun_batch_stdin(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, stderr := newRC(t, rt, output.FormatTable, strings.NewReader("r1\thi\n"))

	if err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: "-"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rt.posted) != 1 || rt.posted[0] != "r1" {
		t.Fatalf("posted = %v, want [r1] from stdin", rt.posted)
	}
	if !strings.Contains(stderr.String(), "OK r1") {
		t.Errorf("stderr should report OK r1; got %q", stderr.String())
	}
}

func TestRun_batch_perLineFailureContinues_aggregateExit30(t *testing.T) {
	// r2 → 404; r1 and r3 succeed. The failure must not abort the batch, and
	// the aggregate exit is the highest code seen (30).
	rt := &replyRT{t: t, status: map[string]int{"r2": 404}}
	rc, _, stderr := newRC(t, rt, output.FormatTable, nil)

	path := writeTSV(t, "r1\ta\nr2\tb\nr3\tc\n")
	err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path})

	if got := rt.posted; len(got) != 3 {
		t.Fatalf("all 3 rows must be attempted despite the failure; posted=%v", got)
	}
	if code := exitCodeOf(t, err); code != 30 {
		t.Errorf("aggregate exit = %d, want 30 (highest code seen)", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "OK r1") || !strings.Contains(out, "OK r3") {
		t.Errorf("successful rows should report OK; got %q", out)
	}
	if !strings.Contains(out, "ERR r2") {
		t.Errorf("failed row should report ERR r2; got %q", out)
	}
}

func TestRun_batch_jsonEnvelope(t *testing.T) {
	rt := &replyRT{t: t, status: map[string]int{"r2": 404}}
	rc, stdout, _ := newRC(t, rt, output.FormatJSON, nil)

	path := writeTSV(t, "r1\ta\nr2\tb\n")
	_ = Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path})

	var env struct {
		Results []struct {
			ReviewID string `json:"reviewId"`
			Status   string `json:"status"`
			Error    string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not the {\"results\":[...]} envelope: %v\n%s", err, stdout.String())
	}
	if len(env.Results) != 2 {
		t.Fatalf("envelope should carry 2 rows, got %d", len(env.Results))
	}
	if env.Results[0].ReviewID != "r1" || env.Results[0].Status != "ok" {
		t.Errorf("row 0 = %+v, want r1/ok", env.Results[0])
	}
	if env.Results[1].ReviewID != "r2" || env.Results[1].Status != "error" || env.Results[1].Error == "" {
		t.Errorf("row 1 = %+v, want r2/error with a message", env.Results[1])
	}
}

func TestRun_batch_malformedLine_reportedAndContinues(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, stderr := newRC(t, rt, output.FormatTable, nil)

	// The middle line has no tab → malformed; r1 and r3 still post.
	path := writeTSV(t, "r1\ta\nbroken-no-tab\nr3\tc\n")
	err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path})

	if got := rt.posted; len(got) != 2 || got[0] != "r1" || got[1] != "r3" {
		t.Fatalf("valid rows should still post around a malformed line; posted=%v", got)
	}
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("a malformed line should push the aggregate exit to 2; got %d", code)
	}
	if !strings.Contains(stderr.String(), "ERR") {
		t.Errorf("malformed line should be reported with ERR; got %q", stderr.String())
	}
}

func TestRun_batch_dryRun_noNetwork(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, stderr := newRC(t, rt, output.FormatTable, nil)

	path := writeTSV(t, "r1\ta\nr2\tb\n")
	if err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path, DryRun: true}); err != nil {
		t.Fatalf("dry-run batch should exit 0; got %v", err)
	}
	if rt.tokenHits != 0 || len(rt.posted) != 0 {
		t.Errorf("--dry-run must not touch the network; posted=%v tokenHits=%d", rt.posted, rt.tokenHits)
	}
	if !strings.Contains(stderr.String(), "DRY-RUN") {
		t.Errorf("dry-run should preview the planned actions; got %q", stderr.String())
	}
}

func TestRun_batch_empty_exit2(t *testing.T) {
	rt := &replyRT{t: t}
	rc, _, _ := newRC(t, rt, output.FormatTable, nil)

	path := writeTSV(t, "# only a comment\n\n")
	err := Run(rc, Input{Package: "com.example.app", BatchSet: true, Batch: path})
	if code := exitCodeOf(t, err); code != 2 {
		t.Errorf("empty batch: exit = %d, want 2", code)
	}
}
