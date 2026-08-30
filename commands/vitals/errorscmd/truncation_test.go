package errorscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// pagingRT serves a fixed sequence of page bodies, so a test can hand the
// client a stream that still has a nextPageToken when --limit cuts it off. It
// answers the OAuth token exchange like the other RoundTrippers in this package
// so nothing here touches the network.
type pagingRT struct {
	mu    sync.Mutex
	pages []string
	n     int
}

func (r *pagingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := http.Header{"Content-Type": []string{"application/json"}}
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`{"access_token":"a","token_type":"Bearer","expires_in":3600}`))}, nil
	}
	body := "{}"
	if r.n < len(r.pages) {
		body = r.pages[r.n]
	}
	r.n++
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func newPagingRC(t *testing.T, pages []string) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: &pagingRT{pages: pages}})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, &stderr
}

// truncatedPages is a stream the server has not finished handing out: page 2
// still advertises a nextPageToken, so a --limit that stops here is a prefix of
// the truth.
var truncatedPages = []string{
	`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
	`{"errorIssues":[{"type":"ANR"},{"type":"ANR"}],"nextPageToken":"P3"}`,
	`{"errorIssues":[{"type":"NON_FATAL"}]}`,
}

// exhaustivePages ends cleanly: the last page carries no token, so a --limit
// that happens to match the total is NOT a truncation.
var exhaustivePages = []string{
	`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
	`{"errorIssues":[{"type":"ANR"}]}`,
}

// TestRunIssues_truncatedListWarnsOnStderr is the user-facing half of PRD #446
// / #451: when --limit cuts a listing short while the server still had pages,
// the command says so on stderr and names the flag to raise, so an agent does
// not read a capped list as the whole truth.
func TestRunIssues_truncatedListWarnsOnStderr(t *testing.T) {
	rc, stderr := newPagingRC(t, truncatedPages)
	payload, err := runIssues(rc, issuesInput{Package: "com.example.app", Limit: 3})
	if err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	got := stderr.String()
	for _, want := range []string{"warning:", "truncated", "--limit"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q, want it to contain %q", got, want)
		}
	}

	// stdout stays a verbatim API mirror (ADR-0003): the warning must not
	// leak into the data channel, and the payload must still hold exactly the
	// capped item count.
	var out bytes.Buffer
	if err := payload.Renderers().JSON(&out); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	if strings.Contains(out.String(), "warning") || strings.Contains(out.String(), "truncated") {
		t.Errorf("stdout = %q, want no trace of the stderr warning", out.String())
	}
	var envelope struct {
		ErrorIssues []json.RawMessage `json:"errorIssues"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not the API envelope: %v", err)
	}
	if len(envelope.ErrorIssues) != 3 {
		t.Errorf("stdout carried %d issues, want 3 (the --limit)", len(envelope.ErrorIssues))
	}
}

// TestRunIssues_exhaustiveListDoesNotWarn is the other half: a listing that
// consumed the whole stream must stay silent about truncation, or the warning
// becomes noise nobody reads. The reporting-delay note stays, so the assertion
// targets the truncation wording specifically.
func TestRunIssues_exhaustiveListDoesNotWarn(t *testing.T) {
	rc, stderr := newPagingRC(t, exhaustivePages)
	if _, err := runIssues(rc, issuesInput{Package: "com.example.app", Limit: 3}); err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	if strings.Contains(stderr.String(), "truncated") {
		t.Errorf("stderr = %q, want no truncation warning (the stream was exhausted)", stderr.String())
	}
}

// TestRunIssues_stdoutIdenticalWithAndWithoutTheWarning pins the ADR-0003
// invariant directly: the same page stream rendered with a truncating --limit
// and with the warning path exercised produces byte-identical stdout to the
// same items fetched without the cap being the reason to stop. Only stderr
// differs.
func TestRunIssues_stdoutIdenticalWithAndWithoutTheWarning(t *testing.T) {
	// Same three items either way: once reached via a --limit 3 that leaves a
	// token behind (warns), once via a stream that simply ends (silent).
	warned, warnedErr := renderIssues(t, truncatedPages, 3)
	silentPages := []string{
		`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
		`{"errorIssues":[{"type":"ANR"}]}`,
	}
	silent, silentErr := renderIssues(t, silentPages, 0)

	if warned != silent {
		t.Errorf("stdout differs between the warned and silent runs:\n warned = %s\n silent = %s", warned, silent)
	}
	if !strings.Contains(warnedErr, "truncated") {
		t.Errorf("warned run stderr = %q, want a truncation warning", warnedErr)
	}
	if strings.Contains(silentErr, "truncated") {
		t.Errorf("silent run stderr = %q, want no truncation warning", silentErr)
	}
}

// renderIssues runs the issues command over pages with the given limit and
// returns (stdout JSON, stderr).
func renderIssues(t *testing.T, pages []string, limit int) (string, string) {
	t.Helper()
	rc, stderr := newPagingRC(t, pages)
	payload, err := runIssues(rc, issuesInput{Package: "com.example.app", Limit: limit})
	if err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	var out bytes.Buffer
	if err := payload.Renderers().JSON(&out); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	return out.String(), stderr.String()
}
