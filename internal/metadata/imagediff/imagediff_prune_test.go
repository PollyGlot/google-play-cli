package imagediff_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func deleteShas(changes []imagediff.SlotChange) []string {
	var out []string
	for _, c := range changes {
		if c.Op == imagediff.OpDelete {
			out = append(out, c.Sha256)
		}
	}
	return out
}

// TestPrune_pureDelete_deletesOnlineOnlyByID: local omits a live image, same
// relative order, nothing new → delete the online-only image by id (no
// deleteall, no reorder).
func TestPrune_pureDelete_deletesOnlineOnlyByID(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: [][]byte{B("a")},
		Live:  []images.Image{img("id-a", B("a")), img("id-b", B("b"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, true)[0]
	if p.Kind != imagediff.KindDeleteIDs {
		t.Fatalf("pure prune-delete should be KindDeleteIDs, got %v", p.Kind)
	}
	if len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "id-b" {
		t.Errorf("should delete the online-only image id-b, got %v", p.DeleteIDs)
	}
	if got := deleteShas(p.Changes); len(got) != 1 || got[0] != sha(B("b")) {
		t.Errorf("delete record should carry b's sha256, got %v", got)
	}
	if hasOp(p.Changes, imagediff.OpReorder) {
		t.Errorf("a pure delete must not reorder: %v", opsOf(p.Changes))
	}
}

// TestPrune_replaceAndDelete_rewrites: local adds a new image and drops a live
// one (content differs) → rewrite (deleteall + reupload local) with delete +
// upload records.
func TestPrune_replaceAndDelete_rewrites(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: [][]byte{B("a"), B("c")},
		Live:  []images.Image{img("id-a", B("a")), img("id-b", B("b"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, true)[0]
	if p.Kind != imagediff.KindRewrite {
		t.Fatalf("prune with a genuine add should rewrite, got %v", p.Kind)
	}
	if !hasOp(p.Changes, imagediff.OpDelete) || !hasOp(p.Changes, imagediff.OpUpload) {
		t.Errorf("should emit both delete (b) and upload (c): %v", opsOf(p.Changes))
	}
	if got := deleteShas(p.Changes); len(got) != 1 || got[0] != sha(B("b")) {
		t.Errorf("delete record should be b's sha, got %v", got)
	}
}

// TestPrune_neverTouchesUnmanagedSlot: a slot empty on disk but live on Play is
// left untouched even under --prune (missing == empty; full-slot clear is out
// of scope, ADR-0013 §3).
func TestPrune_neverTouchesUnmanagedSlot(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Live: []images.Image{img("id-a", B("a"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, true)[0]
	if p.Kind != imagediff.KindNone {
		t.Errorf("--prune must not touch an unmanaged slot, got kind %v", p.Kind)
	}
	if hasOp(p.Changes, imagediff.OpDelete) {
		t.Errorf("--prune must never delete from an unmanaged slot: %v", opsOf(p.Changes))
	}
	if !hasOp(p.Changes, imagediff.OpUntouchedSlot) {
		t.Errorf("unmanaged-but-live slot stays untouchedSlot under prune: %v", opsOf(p.Changes))
	}
}

// TestPrune_offByDefault_keepsOnlineOnly: the same slot WITHOUT prune keeps the
// online-only image (additive): the #134 guarantee, re-asserted via the new
// signature.
func TestPrune_offByDefault_keepsOnlineOnly(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: [][]byte{B("a")},
		Live:  []images.Image{img("id-a", B("a")), img("id-b", B("b"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if hasOp(p.Changes, imagediff.OpDelete) {
		t.Errorf("without --prune the online-only image must be kept: %v", opsOf(p.Changes))
	}
}
