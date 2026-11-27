package edits_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const commitPkg = "com.example.app"

type commitReply struct {
	status  int
	body    string
	dropped bool
}

func editLifecycle(commit commitReply) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case strings.HasSuffix(c.Path, ":commit"):
			return commit.status, commit.body, !commit.dropped
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"edit-1"}`, true
		case c.Method == http.MethodDelete:
			return http.StatusNoContent, "", true
		}
		return 0, "", false
	})
}

func commitCall(t *testing.T, f *testkit.Fake) testkit.Call {
	t.Helper()
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.Path, ":commit") {
			return c
		}
	}
	t.Fatalf("no edits.commit call recorded; calls = %+v", f.Calls())
	return testkit.Call{}
}

func TestCommit_queryParameters(t *testing.T) {
	cases := []struct {
		name string
		opts edits.CommitOptions
		want url.Values
	}{
		{"default sends nothing", edits.CommitOptions{}, url.Values{}},
		{"cancel", edits.CommitOptions{ChangesInReview: edits.ChangesInReviewCancel},
			url.Values{"changesInReviewBehavior": {"CANCEL_IN_REVIEW_AND_SUBMIT"}}},
		{"error", edits.CommitOptions{ChangesInReview: edits.ChangesInReviewError},
			url.Values{"changesInReviewBehavior": {"ERROR_IF_IN_REVIEW"}}},
		{"not sent for review", edits.CommitOptions{ChangesNotSentForReview: true},
			url.Values{"changesNotSentForReview": {"true"}}},
		{"both", edits.CommitOptions{ChangesInReview: edits.ChangesInReviewError, ChangesNotSentForReview: true},
			url.Values{"changesInReviewBehavior": {"ERROR_IF_IN_REVIEW"}, "changesNotSentForReview": {"true"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/implicit", func(t *testing.T) {
			f := editLifecycle(commitReply{status: http.StatusOK, body: `{"id":"edit-1"}`})
			err := edits.WithEdit(context.Background(), &http.Client{Transport: f}, commitPkg,
				edits.Options{Commit: tc.opts}, func(string) error { return nil })
			if err != nil {
				t.Fatalf("WithEdit: %v", err)
			}
			assertQuery(t, commitCall(t, f).Query, tc.want)
		})
		t.Run(tc.name+"/explicit", func(t *testing.T) {
			f := editLifecycle(commitReply{status: http.StatusOK, body: `{"id":"edit-9"}`})
			if err := edits.CommitExplicit(context.Background(), &http.Client{Transport: f}, commitPkg, "edit-9", tc.opts); err != nil {
				t.Fatalf("CommitExplicit: %v", err)
			}
			assertQuery(t, commitCall(t, f).Query, tc.want)
		})
	}
}

func assertQuery(t *testing.T, raw string, want url.Values) {
	t.Helper()
	got, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	if got.Encode() != want.Encode() {
		t.Errorf("commit query = %q, want %q", got.Encode(), want.Encode())
	}
}

func TestParseChangesInReview(t *testing.T) {
	for _, ok := range []string{"", "cancel", "error"} {
		if _, err := edits.ParseChangesInReview(ok); err != nil {
			t.Errorf("ParseChangesInReview(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"CANCEL", "ERROR_IF_IN_REVIEW", "skip"} {
		if _, err := edits.ParseChangesInReview(bad); err == nil {
			t.Errorf("ParseChangesInReview(%q) accepted, want refused", bad)
		}
	}
}

func TestCommit_outcomeUnknown(t *testing.T) {
	cases := []struct {
		name     string
		dropped  bool
		wantExit int
	}{
		{"5xx", false, 40},
		{"transport failure after send", true, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := editLifecycle(commitReply{status: http.StatusServiceUnavailable, body: `{"error":{"code":503,"message":"backend error"}}`, dropped: tc.dropped})
			err := edits.WithEdit(context.Background(), &http.Client{Transport: f}, commitPkg,
				edits.Options{}, func(string) error { return nil })
			var unknown *edits.CommitOutcomeUnknownError
			if !errors.As(err, &unknown) {
				t.Fatalf("err = %v (%T), want *CommitOutcomeUnknownError", err, err)
			}
			d := exit.Classify(err)
			if d.ExitCode != tc.wantExit {
				t.Errorf("exit = %d, want %d (the code is kept)", d.ExitCode, tc.wantExit)
			}
			if d.Code != exit.CodeCommitOutcomeUnknown || d.Retryable {
				t.Errorf("diagnostic = %s retryable=%v, want COMMIT_OUTCOME_UNKNOWN retryable=false", d.Code, d.Retryable)
			}
			if d.Operation != "edits.commit" {
				t.Errorf("operation = %q, want edits.commit (the api.Error stays reachable)", d.Operation)
			}
			if !strings.Contains(err.Error(), "gplay releases list") {
				t.Errorf("message %q does not say how to check the outcome", err.Error())
			}
			last := f.Calls()[len(f.Calls())-1]
			if last.Method != http.MethodDelete {
				t.Errorf("last call = %s %s, want the edits.delete cleanup", last.Method, last.Path)
			}
		})
	}
}

func TestCommitExplicit_outcomeUnknownPointsAtStatusLive(t *testing.T) {
	f := editLifecycle(commitReply{status: http.StatusInternalServerError})
	err := edits.CommitExplicit(context.Background(), &http.Client{Transport: f}, commitPkg, "edit-9", edits.CommitOptions{})
	var unknown *edits.CommitOutcomeUnknownError
	if !errors.As(err, &unknown) || !unknown.Explicit {
		t.Fatalf("err = %v (%T), want an explicit *CommitOutcomeUnknownError", err, err)
	}
	if !strings.Contains(err.Error(), "gplay edits status --live") {
		t.Errorf("message %q does not point at `gplay edits status --live`", err.Error())
	}
}

func TestCommit_definiteFailuresStayPlain(t *testing.T) {
	t.Run("400", func(t *testing.T) {
		f := editLifecycle(commitReply{status: http.StatusBadRequest, body: `{"error":{"code":400,"message":"bad"}}`})
		err := edits.CommitExplicit(context.Background(), &http.Client{Transport: f}, commitPkg, "edit-9", edits.CommitOptions{})
		assertPlain(t, err, 30, exit.CodeInvalidArgument)
	})
	t.Run("429", func(t *testing.T) {
		f := editLifecycle(commitReply{status: http.StatusTooManyRequests, body: `{"error":{"code":429,"message":"slow down"}}`})
		err := edits.CommitExplicit(context.Background(), &http.Client{Transport: f}, commitPkg, "edit-9", edits.CommitOptions{})
		assertPlain(t, err, 60, exit.CodeRateLimitExceeded)
	})
	t.Run("dns", func(t *testing.T) {
		hc := &http.Client{Transport: failingRT(&net.DNSError{Err: "no such host", Name: "androidpublisher.googleapis.com"})}
		err := edits.CommitExplicit(context.Background(), hc, commitPkg, "edit-9", edits.CommitOptions{})
		assertPlain(t, err, 50, exit.CodeNetworkError)
	})
	t.Run("dial", func(t *testing.T) {
		hc := &http.Client{Transport: failingRT(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})}
		err := edits.CommitExplicit(context.Background(), hc, commitPkg, "edit-9", edits.CommitOptions{})
		assertPlain(t, err, 50, exit.CodeNetworkError)
	})
	t.Run("token refused", func(t *testing.T) {
		hc := &http.Client{Transport: failingRT(&token.AuthError{StatusCode: 400, Body: "invalid_grant"})}
		err := edits.CommitExplicit(context.Background(), hc, commitPkg, "edit-9", edits.CommitOptions{})
		assertPlain(t, err, 10, exit.CodeAuthFailed)
	})
}

func assertPlain(t *testing.T, err error, wantExit int, wantCode exit.Code) {
	t.Helper()
	var unknown *edits.CommitOutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Fatalf("err = %v, want a plain failure, not an unknown outcome", err)
	}
	d := exit.Classify(err)
	if d.ExitCode != wantExit || d.Code != wantCode {
		t.Errorf("classified exit=%d code=%s, want exit=%d code=%s (err: %v)", d.ExitCode, d.Code, wantExit, wantCode, err)
	}
}

func failingRT(err error) http.RoundTripper {
	return testkit.RoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, err })
}
