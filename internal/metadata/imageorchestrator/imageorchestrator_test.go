// Package imageorchestrator_test exercises the Edit-lifecycle guarantees of
// `images apply`: the atomic single-Edit commit, auto-discard on a per-slot
// failure (0 published), and the no-op discard (no empty commit). It drives
// orchestrator.Apply directly with a fake transport: no kernel/auth needed.
package imageorchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// orchRT is a fake transport with a per-slot live body and a toggle to fail
// uploads. It records whether the Edit was committed or discarded.
type orchRT struct {
	t          *testing.T
	editID     string
	liveBody   string // images.list body for every slot
	failUpload bool
	failCommit bool // Play rejects the commit (e.g. a required slot dropped)

	mu            sync.Mutex
	committed     bool
	discarded     bool
	uploads       int
	deleteAll     int
	singleDeletes int
}

func (r *orchRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/edits"):
		return resp(200, fmt.Sprintf(`{"id":%q}`, r.editID)), nil
	case strings.HasSuffix(req.URL.Path, ":commit"):
		if r.failCommit {
			return resp(400, `{"error":{"message":"app listing requires at least 2 phone screenshots"}}`), nil
		}
		r.committed = true
		return resp(200, `{}`), nil
	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/upload/"):
		r.uploads++
		_, _ = io.Copy(io.Discard, req.Body)
		if r.failUpload {
			return resp(400, `{"error":{"message":"bad image"}}`), nil
		}
		return resp(200, `{"image":{"id":"up","sha256":"s"}}`), nil
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/listings/"):
		body := r.liveBody
		if body == "" {
			body = `{"images":[]}`
		}
		return resp(200, body), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/listings/"):
		// .../listings/<locale>/<type>      → deleteall (2 segments)
		// .../listings/<locale>/<type>/<id> → single image delete (3 segments)
		tail := req.URL.Path[strings.Index(req.URL.Path, "/listings/")+len("/listings/"):]
		if strings.Count(tail, "/") >= 2 {
			r.singleDeletes++
		} else {
			r.deleteAll++
		}
		return resp(200, `{"deleted":[]}`), nil
	case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/edits/"):
		r.discarded = true
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	r.t.Fatalf("unexpected: %s %s", req.Method, req.URL)
	return nil, nil
}

func resp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// TestApply_perSlotFailure_discardsEdit asserts a failed upload aborts the
// whole apply: the Edit is discarded (auto-rollback), never committed: 0 slots
// published (atomic).
func TestApply_perSlotFailure_discardsEdit(t *testing.T) {
	rt := &orchRT{t: t, editID: "e", failUpload: true}
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.Icon: {[]byte("icon-bytes")}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"icon"}, NoValidate: true,
	})
	if err == nil {
		t.Fatal("want error when an upload fails")
	}
	if rt.committed {
		t.Error("a failed apply must NOT commit (atomic rollback)")
	}
	if !rt.discarded {
		t.Error("a failed apply must discard the Edit")
	}
}

// TestApply_noOp_discardsWithoutCommit asserts that when the live slot already
// matches local, the real apply discards the Edit instead of committing an
// empty one (Google quota conservation).
func TestApply_noOp_discardsWithoutCommit(t *testing.T) {
	iconBytes := []byte("icon-bytes")
	live := fmt.Sprintf(`{"images":[{"id":"x","sha256":%q}]}`, sha(iconBytes))
	rt := &orchRT{t: t, editID: "e", liveBody: live}
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.Icon: {iconBytes}}}

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"icon"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("no-op apply should not error: %v", err)
	}
	if rt.committed {
		t.Error("a no-op apply must NOT commit an empty Edit")
	}
	if rt.uploads != 0 {
		t.Errorf("no-op apply uploaded %d, want 0", rt.uploads)
	}
	if res.Diff.HasChanges() {
		t.Errorf("no-op diff should report no changes: %+v", res.Diff.Summary)
	}
}

// TestApply_prune_deletesOnlineOnly asserts --prune removes a managed slot's
// online-only image by id (a single delete, no deleteall) and commits.
func TestApply_prune_deletesOnlineOnly(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := &orchRT{t: t, editID: "e", liveBody: live}
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}} // keep only a; b is online-only

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Prune: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("prune apply: %v", err)
	}
	if rt.singleDeletes != 1 {
		t.Errorf("prune should delete the 1 online-only image by id, got %d single deletes", rt.singleDeletes)
	}
	if rt.deleteAll != 0 {
		t.Errorf("a pure prune-delete should not deleteall, got %d", rt.deleteAll)
	}
	if !rt.committed {
		t.Error("prune apply should commit")
	}
	if res.Diff.Summary.Delete != 1 {
		t.Errorf("diff summary.delete = %d, want 1", res.Diff.Summary.Delete)
	}
}

// TestApply_prune_offWithoutFlag_keepsOnlineOnly asserts the same tree WITHOUT
// --prune deletes nothing and commits no change (additive no-op).
func TestApply_prune_offWithoutFlag_keepsOnlineOnly(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := &orchRT{t: t, editID: "e", liveBody: live}
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("additive apply: %v", err)
	}
	if rt.singleDeletes != 0 || rt.deleteAll != 0 {
		t.Errorf("additive (no --prune) must delete nothing, got %d single / %d deleteall", rt.singleDeletes, rt.deleteAll)
	}
	if rt.committed {
		t.Error("additive no-op (local ⊆ live) must not commit")
	}
}

// TestApply_prune_belowRequiredMinimum_rejectedAtCommit asserts the
// required-slot safety: a prune Play rejects at commit (e.g. dropping below the
// minimum screenshot count) leaves the Edit auto-discarded, store untouched.
func TestApply_prune_belowRequiredMinimum_rejectedAtCommit(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := &orchRT{t: t, editID: "e", liveBody: live, failCommit: true}
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Prune: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err == nil {
		t.Fatal("want the commit rejection to surface as an error")
	}
	if rt.committed {
		t.Error("a rejected commit must not be recorded as committed")
	}
	if !rt.discarded {
		t.Error("a rejected commit must auto-discard the Edit (store untouched)")
	}
}
