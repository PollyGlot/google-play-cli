package commit_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	commitcmd "github.com/PollyGlot/google-play-cli/commands/edits/commit"
	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/editpin"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const pkg = "com.example.app"

// newCommitFake serves the edits.commit POST and an edits.insert POST (served
// so a regression that opens a replacement Edit is counted, not just
// refused); commitStatus (0 → 200) forces a non-2xx for the failure test.
func newCommitFake(commitStatus int) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case strings.HasSuffix(c.Path, ":commit"):
			if commitStatus != 0 {
				return commitStatus, `{"error":{"code":400,"message":"validation failed"}}`, true
			}
			return 200, `{"id":"edit-9","expiryTimeSeconds":"0"}`, true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, `{"id":"edit-new"}`, true
		}
		return 0, "", false
	})
}

// countCalls returns the edits.commit and edits.insert calls, and fails the
// test on any other request (even where Run's error would hide it).
func countCalls(t *testing.T, f *testkit.Fake) (commits, inserts int) {
	t.Helper()
	for _, c := range f.Calls() {
		switch {
		case strings.HasSuffix(c.Path, ":commit"):
			commits++
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			inserts++
		default:
			t.Errorf("unexpected request: %s %s", c.Method, c.Path)
		}
	}
	return commits, inserts
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, string) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gplayDir := filepath.Join(t.TempDir(), ".gplay")
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	rc.Resolved = &config.Resolved{Pin: pkg, ProjectSharedPath: filepath.Join(gplayDir, "config.json")}
	return rc, gplayDir
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) {
		t.Fatalf("err %v (%T) has no ExitCode", err, err)
	}
	return c.ExitCode()
}

func TestRun_commitsAndClearsPin(t *testing.T) {
	fake := newCommitFake(0)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	commits, inserts := countCalls(t, fake)
	if commits != 1 {
		t.Errorf("commitCalls = %d, want 1", commits)
	}
	// Explicit-lifecycle contract: commit must never open a replacement Edit.
	if inserts != 0 {
		t.Errorf("commit opened %d Edit(s); want 0 (it commits the pinned Edit)", inserts)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin still present after a successful commit")
	}
}

func TestRun_noOpenEdit_exit60_noNetwork(t *testing.T) {
	fake := newCommitFake(0)
	rc, _ := newRC(t, fake)

	_, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg})
	if code := exitOf(t, err); code != 60 {
		t.Fatalf("exit = %d, want 60 (no open edit)", code)
	}
	if commits, _ := countCalls(t, fake); commits != 0 {
		t.Errorf("no open edit must fail before the network; commitCalls = %d", commits)
	}
}

func TestRun_commitFails_leavesPinInPlace(t *testing.T) {
	fake := newCommitFake(400)
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if _, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg}); err == nil {
		t.Fatal("expected a commit error")
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("a failed commit must leave the pin in place for a retry/discard")
	}
	countCalls(t, fake)
}

// TestRun_forwardsCommitOptIns: the #598 opt-ins reach edits.commit as
// Discovery's query parameters, and a successful commit still clears the pin.
func TestRun_forwardsCommitOptIns(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		if strings.HasSuffix(c.Path, ":commit") {
			return http.StatusOK, `{"id":"edit-9"}`, true
		}
		return 0, "", false
	})
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	in := commitcmd.Input{Package: pkg, Commit: commitflags.Flags{ChangesInReview: edits.ChangesInReviewError, ChangesNotSentForReview: true}}
	if _, err := commitcmd.Run(rc, in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the single edits.commit", calls)
	}
	const want = "changesInReviewBehavior=ERROR_IF_IN_REVIEW&changesNotSentForReview=true"
	if calls[0].Query != want {
		t.Errorf("commit query = %q, want %q", calls[0].Query, want)
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); ok {
		t.Error("pin still present after a successful commit")
	}
}

// TestRun_unknownOutcome_keepsPinAndIsNotRetryable: a 5xx on the commit may
// have published, so the pin stays (the Edit may still be open) and the
// failure is COMMIT_OUTCOME_UNKNOWN rather than a retry-safe 40.
func TestRun_unknownOutcome_keepsPinAndIsNotRetryable(t *testing.T) {
	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		return http.StatusBadGateway, `{"error":{"code":502,"message":"bad gateway"}}`, true
	})
	rc, gplayDir := newRC(t, fake)
	if err := editpin.Write(config.OSFS{}, gplayDir, pkg, "edit-9"); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	_, err := commitcmd.Run(rc, commitcmd.Input{Package: pkg})
	d := exit.Classify(err)
	if d.ExitCode != 40 || d.Code != exit.CodeCommitOutcomeUnknown || d.Retryable {
		t.Errorf("diagnostic = exit %d %s retryable=%v, want exit 40 COMMIT_OUTCOME_UNKNOWN retryable=false", d.ExitCode, d.Code, d.Retryable)
	}
	if !strings.Contains(err.Error(), "gplay edits status --live") {
		t.Errorf("message %q does not say how to check the outcome", err.Error())
	}
	if _, ok, _ := editpin.Lookup(config.OSFS{}, gplayDir, pkg); !ok {
		t.Error("an unknown outcome must leave the pin in place")
	}
}
