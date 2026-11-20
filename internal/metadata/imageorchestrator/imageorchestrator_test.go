package imageorchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func stage(t *testing.T, tr imagetree.Tree) (string, imagetree.Staged) {
	t.Helper()
	dir := t.TempDir()
	st := make(imagetree.Staged, len(tr))
	for loc, slots := range tr {
		st[loc] = make(map[images.Type][]string, len(slots))
		for ty, seq := range slots {
			for i, data := range seq {
				p := filepath.Join(dir, fmt.Sprintf("%s-%s-%d", loc, ty, i))
				if err := os.WriteFile(p, data, 0o600); err != nil {
					t.Fatal(err)
				}
				st[loc][ty] = append(st[loc][ty], p)
			}
		}
	}
	return dir, st
}

type orchRT struct {
	editID     string
	liveBody   string
	failUpload bool
	failCommit bool
}

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

func TestApply_perSlotFailure_discardsEdit(t *testing.T) {
	rt := newOrch(t, orchRT{editID: "e", failUpload: true})
	hc := &http.Client{Transport: rt}
	dir, local := stage(t, imagetree.Tree{"en-US": {images.Icon: {[]byte("icon-bytes")}}})

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Dir:     dir,
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

func TestApply_noOp_discardsWithoutCommit(t *testing.T) {
	iconBytes := []byte("icon-bytes")
	live := fmt.Sprintf(`{"images":[{"id":"x","sha256":%q}]}`, sha(iconBytes))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	dir, local := stage(t, imagetree.Tree{"en-US": {images.Icon: {iconBytes}}})

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Dir:     dir,
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

func TestApply_prune_deletesOnlineOnly(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	dir, local := stage(t, imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}})

	res, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Dir:     dir,
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

func TestApply_prune_offWithoutFlag_keepsOnlineOnly(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live})
	hc := &http.Client{Transport: rt}
	dir, local := stage(t, imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}})

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Dir:     dir,
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

func TestApply_prune_belowRequiredMinimum_rejectedAtCommit(t *testing.T) {
	a := []byte("shot-a")
	live := fmt.Sprintf(`{"images":[{"id":"id-a","sha256":%q},{"id":"id-b","sha256":%q}]}`, sha(a), sha([]byte("shot-b")))
	rt := newOrch(t, orchRT{editID: "e", liveBody: live, failCommit: true})
	hc := &http.Client{Transport: rt}
	dir, local := stage(t, imagetree.Tree{"en-US": {images.PhoneScreenshots: {a}}})

	_, err := imageorchestrator.Apply(context.Background(), hc, local, imageorchestrator.Opts{
		Dir:     dir,
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

func TestApply_aiGenerated_ridesEveryUpload(t *testing.T) {
	for _, ai := range []images.AIGeneratedState{"", images.AIGenerated, images.NotAIGenerated} {
		t.Run(string(ai), func(t *testing.T) {
			rt := newOrch(t, orchRT{editID: "e"})
			dir, local := stage(t, imagetree.Tree{"en-US": {images.PhoneScreenshots: {[]byte("shot-a"), []byte("shot-b")}}})
			if _, err := imageorchestrator.Apply(context.Background(), &http.Client{Transport: rt}, local, imageorchestrator.Opts{
				Dir: dir, Package: "com.example.app", Confirm: true, NoValidate: true, AIGenerated: ai,
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if rt.uploads() != 2 {
				t.Fatalf("uploads = %d, want 2", rt.uploads())
			}
			for _, c := range rt.Calls() {
				if c.Method != http.MethodPost || !strings.HasPrefix(c.Path, "/upload/") {
					continue
				}
				q, err := url.ParseQuery(c.Query)
				if err != nil {
					t.Fatalf("parse query: %v", err)
				}
				if _, sent := q["aiGeneratedState"]; sent != (ai != "") || q.Get("aiGeneratedState") != string(ai) {
					t.Errorf("upload %s?%s: aiGeneratedState = %q (sent %v), want %q", c.Path, c.Query, q.Get("aiGeneratedState"), sent, ai)
				}
			}
		})
	}
}
