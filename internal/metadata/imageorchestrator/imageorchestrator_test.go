// Package imageorchestrator_test exercises the Edit-lifecycle guarantees of
// `images apply`: the atomic single-Edit commit, auto-discard on a per-slot
// failure (0 published), and the no-op discard (no empty commit). It drives
// orchestrator.Apply directly with a testkit.Fake: no kernel/auth needed.
package imageorchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// orchRT configures a testkit.Fake with a per-slot live body and a toggle to
// fail uploads. What the Edit went through (committed, discarded, uploads,
// deletes) is read back from the recorded calls.
type orchRT struct {
	editID     string
	liveBody   string // images.list body for every slot
	failUpload bool
	failCommit bool // Play rejects the commit (e.g. a required slot dropped)
}

// orchFake is the Fake plus the one setting the call log cannot tell: whether
// the commit it answered was accepted.
type orchFake struct {
	*testkit.Fake
	failCommit bool
}

func newOrch(t *testing.T, r orchRT) orchFake {
	t.Helper()
	return orchFake{failCommit: r.failCommit, Fake: testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 200, fmt.Sprintf(`{"id":%q}`, r.editID), true
		case strings.HasSuffix(c.Path, ":commit"):
			if r.failCommit {
				return 400, `{"error":{"message":"app listing requires at least 2 phone screenshots"}}`, true
			}
			return 200, `{}`, true
		case c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/"):
			if r.failUpload {
				return 400, `{"error":{"message":"bad image"}}`, true
			}
			return 200, `{"image":{"id":"up","sha256":"s"}}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			body := r.liveBody
			if body == "" {
				body = `{"images":[]}`
			}
			return 200, body, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/listings/"):
			return 200, `{"deleted":[]}`, true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return 204, ``, true
		}
		t.Errorf("unexpected: %s %s", c.Method, c.Path)
		return 0, "", false
	})}
}

func (f orchFake) count(match func(testkit.Call) bool) int {
	n := 0
	for _, c := range f.Calls() {
		if match(c) {
			n++
		}
	}
	return n
}

func (f orchFake) committed() bool {
	return !f.failCommit && f.count(func(c testkit.Call) bool { return strings.HasSuffix(c.Path, ":commit") }) > 0
}

func (f orchFake) discarded() bool {
	return f.count(func(c testkit.Call) bool {
		return c.Method == http.MethodDelete && !strings.Contains(c.Path, "/listings/") && strings.Contains(c.Path, "/edits/")
	}) > 0
}

func (f orchFake) uploads() int {
	return f.count(func(c testkit.Call) bool {
		return c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/")
	})
}

// listingDeletes counts the image DELETEs under /listings/ of one kind:
// .../listings/<locale>/<type> is a deleteall,
// .../listings/<locale>/<type>/<id> a single image delete.
func (f orchFake) listingDeletes(single bool) int {
	return f.count(func(c testkit.Call) bool {
		i := strings.Index(c.Path, "/listings/")
		if c.Method != http.MethodDelete || i < 0 {
			return false
		}
		return (strings.Count(c.Path[i+len("/listings/"):], "/") >= 2) == single
	})
}

func (f orchFake) deleteAll() int     { return f.listingDeletes(false) }
func (f orchFake) singleDeletes() int { return f.listingDeletes(true) }

// TestApply_perSlotFailure_discardsEdit asserts a failed upload aborts the
// whole apply: the Edit is discarded (auto-rollback), never committed: 0 slots
// published (atomic).
func TestApply_perSlotFailure_discardsEdit(t *testing.T) {
	rt := newOrch(t, orchRT{editID: "e", failUpload: true})
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.Icon: {[]byte("icon-bytes")}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"icon"}, NoValidate: true,
	})
	if err == nil {
		t.Fatal("want error when an upload fails")
	}
	if rt.committed() {
		t.Error("a failed apply must NOT commit (atomic rollback)")
	}
	if !rt.discarded() {
		t.Error("a failed apply must discard the Edit")
	}
}

// TestApply_noOp_discardsWithoutCommit asserts that when the live slot already
// matches local, the real apply discards the Edit instead of committing an
// empty one (Google quota conservation).
func TestApply_noOp_discardsWithoutCommit(t *testing.T) {
	iconBytes := []byte("icon-bytes")
	live := fmt.Sprintf(`{"images":[{"id":"x","sha256":%q}]}`, sha(iconBytes))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.Icon: {iconBytes}}}

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"icon"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("no-op apply should not error: %v", err)
	}
	if rt.committed() {
		t.Error("a no-op apply must NOT commit an empty Edit")
	}
	if rt.uploads() != 0 {
		t.Errorf("no-op apply uploaded %d, want 0", rt.uploads())
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
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}} // keep only a; b is online-only

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Prune: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("prune apply: %v", err)
	}
	if rt.singleDeletes() != 1 {
		t.Errorf("prune should delete the 1 online-only image by id, got %d single deletes", rt.singleDeletes())
	}
	if rt.deleteAll() != 0 {
		t.Errorf("a pure prune-delete should not deleteall, got %d", rt.deleteAll())
	}
	if !rt.committed() {
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
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err != nil {
		t.Fatalf("additive apply: %v", err)
	}
	if rt.singleDeletes() != 0 || rt.deleteAll() != 0 {
		t.Errorf("additive (no --prune) must delete nothing, got %d single / %d deleteall", rt.singleDeletes(), rt.deleteAll())
	}
	if rt.committed() {
		t.Error("additive no-op (local ⊆ live) must not commit")
	}
}

// TestApply_prune_belowRequiredMinimum_rejectedAtCommit asserts the
// required-slot safety: a prune Play rejects at commit (e.g. dropping below the
// minimum screenshot count) leaves the Edit auto-discarded, store untouched.
func TestApply_prune_belowRequiredMinimum_rejectedAtCommit(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live, failCommit: true})
	hc := &http.Client{Transport: rt}
	local := imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}}

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Package: "com.example.app", Confirm: true, Prune: true, Types: []string{"phoneScreenshots"}, NoValidate: true,
	})
	if err == nil {
		t.Fatal("want the commit rejection to surface as an error")
	}
	if rt.committed() {
		t.Error("a rejected commit must not be recorded as committed")
	}
	if !rt.discarded() {
		t.Error("a rejected commit must auto-discard the Edit (store untouched)")
	}
}
