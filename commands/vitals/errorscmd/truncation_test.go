package errorscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func newPagingRC(t *testing.T, pages []string) (*kernel.RunContext, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(saJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.NewFake(testkit.Sequence(pages...))})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Scope = token.ReportingScope
	return rc, &stderr
}

var truncatedPages = []string{
	`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
	`{"errorIssues":[{"type":"ANR"},{"type":"ANR"}],"nextPageToken":"P3"}`,
	`{"errorIssues":[{"type":"NON_FATAL"}]}`,
}

var exhaustivePages = []string{
	`{"errorIssues":[{"type":"CRASH"},{"type":"CRASH"}],"nextPageToken":"P2"}`,
	`{"errorIssues":[{"type":"ANR"}]}`,
}

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

func TestRunIssues_exhaustiveListDoesNotWarn(t *testing.T) {
	rc, stderr := newPagingRC(t, exhaustivePages)
	if _, err := runIssues(rc, issuesInput{Package: "com.example.app", Limit: 3}); err != nil {
		t.Fatalf("runIssues: %v", err)
	}
	if strings.Contains(stderr.String(), "truncated") {
		t.Errorf("stderr = %q, want no truncation warning (the stream was exhausted)", stderr.String())
	}
}

func TestRunIssues_stdoutIdenticalWithAndWithoutTheWarning(t *testing.T) {
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
