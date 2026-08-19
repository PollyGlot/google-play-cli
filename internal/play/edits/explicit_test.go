package edits_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
)

// noRequestRT fails the test on ANY HTTP request. The explicit-mode
// short-circuit must reach neither edits.insert, :commit, nor edits.delete:
// fn runs directly against the pinned Edit, so the transport stays untouched.
type noRequestRT struct{ t *testing.T }

func (r *noRequestRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Fatalf("explicit mode made an HTTP call it should not: %s %s", req.Method, req.URL)
	return nil, nil
}

func TestWithEdit_explicitMode_reusesPinAndMakesNoLifecycleCalls(t *testing.T) {
	hc := &http.Client{Transport: &noRequestRT{t: t}}
	var gotEditID string
	err := edits.WithEdit(context.Background(), hc, "com.example.app",
		edits.Options{ExplicitEditID: "edit-pinned"},
		func(editID string) error { gotEditID = editID; return nil })
	if err != nil {
		t.Fatalf("WithEdit: %v", err)
	}
	if gotEditID != "edit-pinned" {
		t.Errorf("fn ran against %q, want the pinned edit-pinned", gotEditID)
	}
	// The RoundTripper would have failed the test on any insert/commit/discard.
}

func TestWithEdit_explicitMode_failureDoesNotAutoDiscard(t *testing.T) {
	// In explicit mode a closure failure must leave the Edit open (no
	// edits.delete) so the user can retry or `gplay edits discard`. noRequestRT
	// fails on any call, including the discard WithEdit does in implicit mode.
	hc := &http.Client{Transport: &noRequestRT{t: t}}
	sentinel := errors.New("mutation failed mid-batch")
	err := edits.WithEdit(context.Background(), hc, "com.example.app",
		edits.Options{ExplicitEditID: "edit-pinned"},
		func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithEdit error = %v, want the sentinel propagated verbatim", err)
	}
}

// callRecorderRT records every request method+path and serves a canned Edit
// response so the explicit helpers can be asserted to make EXACTLY their one
// call (open=insert, commit=:commit, discard=delete) and nothing else.
type callRecorderRT struct {
	t      *testing.T
	editID string
	mu     sync.Mutex
	calls  []string
}

func (r *callRecorderRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	return (&editsRT{t: r.t, editID: r.editID}).RoundTrip(req)
}

func TestOpenExplicit_insertsOnceNoCommitNoDiscard(t *testing.T) {
	rt := &callRecorderRT{t: t, editID: "edit-77"}
	id, err := edits.OpenExplicit(context.Background(), &http.Client{Transport: rt}, "com.example.app")
	if err != nil {
		t.Fatalf("OpenExplicit: %v", err)
	}
	if id != "edit-77" {
		t.Errorf("OpenExplicit id = %q, want edit-77", id)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "POST /androidpublisher/v3/applications/com.example.app/edits" {
		t.Errorf("OpenExplicit calls = %v, want a single edits.insert", rt.calls)
	}
}

func TestOpenExplicit_editAlreadyExists_isConflict(t *testing.T) {
	rt := &insertStatusRT{
		t:        t,
		status:   400,
		bodyJSON: `{"error":{"code":400,"message":"Edit ID required","errors":[{"reason":"editAlreadyExists"}]}}`,
	}
	_, err := edits.OpenExplicit(context.Background(), &http.Client{Transport: rt}, "com.example.app")
	var conflict *edits.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("OpenExplicit error = %v (%T), want *EditConflictError", err, err)
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("editAlreadyExists error must expose a Coder; got %T", err)
	}
	if coder.ExitCode() != 30 {
		t.Errorf("editAlreadyExists exit = %d, want 30", coder.ExitCode())
	}
}

func TestCommitExplicit_postsCommitOnly(t *testing.T) {
	rt := &callRecorderRT{t: t, editID: "edit-9"}
	if err := edits.CommitExplicit(context.Background(), &http.Client{Transport: rt}, "com.example.app", "edit-9"); err != nil {
		t.Fatalf("CommitExplicit: %v", err)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "POST /androidpublisher/v3/applications/com.example.app/edits/edit-9:commit" {
		t.Errorf("CommitExplicit calls = %v, want a single edits.commit", rt.calls)
	}
}

func TestDiscardExplicit_deletesOnly(t *testing.T) {
	rt := &callRecorderRT{t: t, editID: "edit-3"}
	if err := edits.DiscardExplicit(context.Background(), &http.Client{Transport: rt}, "com.example.app", "edit-3"); err != nil {
		t.Fatalf("DiscardExplicit: %v", err)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "DELETE /androidpublisher/v3/applications/com.example.app/edits/edit-3" {
		t.Errorf("DiscardExplicit calls = %v, want a single edits.delete", rt.calls)
	}
}
