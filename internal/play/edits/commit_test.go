package edits_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const commitPkg = "com.example.app"

// editLifecycle answers edits.insert with edit-1 and edits.delete with 204, and
// serves edits.commit with commitStatus. A commitStatus of -1 leaves the commit
// unclaimed, which the Fake turns into a transport error: the "request left,
// no answer came back" case.
func editLifecycle(commitStatus int, commitBody string) *testkit.Fake {
	return testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case strings.HasSuffix(c.Path, ":commit"):
			if commitStatus < 0 {
				return 0, "", false
			}
			return commitStatus, commitBody, true
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

// TestCommit_queryParameters pins the wire contract of the opt-ins (#598): the
// zero value sends no query at all (the request gplay has always sent, so
// Google's default holds), and each opt-in maps to Discovery's parameter name
// and enum value, on both the implicit and the explicit commit path.
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
			f := editLifecycle(http.StatusOK, `{"id":"edit-1"}`)
			err := edits.WithEdit(context.Background(), &http.Client{Transport: f}, commitPkg,
				edits.Options{Commit: tc.opts}, func(string) error { return nil })
			if err != nil {
				t.Fatalf("WithEdit: %v", err)
			}
			assertQuery(t, commitCall(t, f).Query, tc.want)
		})
		t.Run(tc.name+"/explicit", func(t *testing.T) {
			f := editLifecycle(http.StatusOK, `{"id":"edit-9"}`)
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
