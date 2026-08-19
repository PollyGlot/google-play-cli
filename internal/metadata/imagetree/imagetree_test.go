// Package imagetree_test exercises the on-disk Store-image codec: the pure,
// filesystem-only translation between an `<dir>/<locale>/images/...` layout
// and the in-memory Tree. Read and Write must be value-level inverses so
// `images pull` followed by `images apply` is a guaranteed no-op (ADR-0013).
package imagetree_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// pngBytes / jpgBytes are minimal blobs carrying the right magic number so
// the codec's byte-sniffing picks the extension (it reads the header, not the
// Content-Type).
func pngBytes(tag string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), tag...) }
func jpgBytes(tag string) []byte { return append([]byte("\xff\xd8\xff\xe0"), tag...) }

// TestWrite_singularSlot_namesByType asserts a singular slot is written as
// `<type>.<ext>` directly under the locale's images/ directory, with the
// extension sniffed from the bytes.
func TestWrite_singularSlot_namesByType(t *testing.T) {
	dir := t.TempDir()
	tr := imagetree.Tree{
		"en-US": {
			images.Icon:           {pngBytes("icon")},
			images.FeatureGraphic: {jpgBytes("feat")},
		},
	}
	if err := imagetree.Write(dir, tr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "icon.png")); err != nil {
		t.Errorf("icon.png not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "featureGraphic.jpg")); err != nil {
		t.Errorf("featureGraphic.jpg not written: %v", err)
	}
}

// TestWrite_gallerySlot_numbersInOrder asserts a gallery slot is written as a
// `<type>/` directory holding `1.<ext>`…`N.<ext>` in the in-memory order (the
// display order), no zero-padding (Play caps galleries at 8).
func TestWrite_gallerySlot_numbersInOrder(t *testing.T) {
	dir := t.TempDir()
	tr := imagetree.Tree{
		"en-US": {
			images.PhoneScreenshots: {pngBytes("one"), jpgBytes("two"), pngBytes("three")},
		},
	}
	if err := imagetree.Write(dir, tr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	base := filepath.Join(dir, "en-US", "images", "phoneScreenshots")
	for _, name := range []string{"1.png", "2.jpg", "3.png"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("gallery file %s missing: %v", name, err)
		}
	}
}

// TestRoundTrip_readInvertsWrite is the core invariant: Read(Write(tree)) ==
// tree (value-level), preserving gallery order. This is what makes pull → apply
// a no-op.
func TestRoundTrip_readInvertsWrite(t *testing.T) {
	dir := t.TempDir()
	tr := imagetree.Tree{
		"en-US": {
			images.Icon:             {pngBytes("icon")},
			images.PhoneScreenshots: {pngBytes("a"), jpgBytes("b"), pngBytes("c")},
		},
		"fr-FR": {
			images.FeatureGraphic: {jpgBytes("fr-feat")},
		},
	}
	if err := imagetree.Write(dir, tr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, tr) {
		t.Errorf("round-trip mismatch:\n got = %v\nwant = %v", summarize(got), summarize(tr))
	}
}

// TestRead_galleryOrderIsFilenameSort asserts gallery files are read back in
// filename-sorted (display) order regardless of directory iteration order.
func TestRead_galleryOrderIsFilenameSort(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "en-US", "images", "phoneScreenshots")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write out of order; Read must return them sorted by name (1,2,3).
	if err := os.WriteFile(filepath.Join(base, "3.png"), pngBytes("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "1.png"), pngBytes("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "2.png"), pngBytes("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	seq := got["en-US"][images.PhoneScreenshots]
	if len(seq) != 3 {
		t.Fatalf("got %d screenshots, want 3", len(seq))
	}
	if !bytes.Equal(seq[0], pngBytes("one")) || !bytes.Equal(seq[1], pngBytes("two")) || !bytes.Equal(seq[2], pngBytes("three")) {
		t.Errorf("gallery not in filename-sorted order")
	}
}

// TestRead_emptyAndStrayAreHarmless asserts an absent images/ dir, a stray
// empty gallery dir, and an unrecognized file are all benign (missing ==
// empty, ADR-0013): never an error, never a phantom slot.
func TestRead_emptyAndStrayAreHarmless(t *testing.T) {
	dir := t.TempDir()
	// A locale with a README and an empty stray gallery dir, no images.
	imgDir := filepath.Join(dir, "en-US", "images")
	if err := os.MkdirAll(filepath.Join(imgDir, "phoneScreenshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "icon.txt"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read should not error on stray/empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("stray/empty tree should yield no managed slots, got %v", summarize(got))
	}
}

// TestRead_absentDir_isEmptyNotError asserts reading a metadata root that has
// no image trees at all yields an empty (non-nil) Tree, no error, so apply
// against a text-only tree is a clean no-op.
func TestRead_absentDir_isEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "en-US"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil tree, got %v", got)
	}
}

// TestWrite_isIdempotent_prunesStaleGallery asserts that re-Writing a slot that
// SHRANK (a gallery that went from 3 images to 2) removes the stale trailing
// file, so Read no longer reports a phantom screenshot. This is what keeps a
// re-`pull` (live shrank) a faithful mirror and `pull → apply` a no-op.
func TestWrite_isIdempotent_prunesStaleGallery(t *testing.T) {
	dir := t.TempDir()
	big := imagetree.Tree{"en-US": {images.PhoneScreenshots: {pngBytes("a"), pngBytes("b"), pngBytes("c")}}}
	if err := imagetree.Write(dir, big); err != nil {
		t.Fatalf("Write big: %v", err)
	}
	small := imagetree.Tree{"en-US": {images.PhoneScreenshots: {pngBytes("a"), pngBytes("b")}}}
	if err := imagetree.Write(dir, small); err != nil {
		t.Fatalf("Write small: %v", err)
	}
	// The stale 3.png must be gone.
	if _, err := os.Stat(filepath.Join(dir, "en-US", "images", "phoneScreenshots", "3.png")); !os.IsNotExist(err) {
		t.Errorf("stale 3.png should have been pruned on re-write")
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n := len(got["en-US"][images.PhoneScreenshots]); n != 2 {
		t.Errorf("gallery should read back 2 images after shrink, got %d (phantom)", n)
	}
}

// TestWrite_isIdempotent_prunesStaleSingularExt asserts that re-Writing a
// singular slot whose extension CHANGED (jpg → png) leaves exactly one file, so
// Read is deterministic (no dual-ext race).
func TestWrite_isIdempotent_prunesStaleSingularExt(t *testing.T) {
	dir := t.TempDir()
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {jpgBytes("v1")}}}); err != nil {
		t.Fatalf("Write jpg: %v", err)
	}
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.Icon: {pngBytes("v2")}}}); err != nil {
		t.Fatalf("Write png: %v", err)
	}
	imgDir := filepath.Join(dir, "en-US", "images")
	if _, err := os.Stat(filepath.Join(imgDir, "icon.jpg")); !os.IsNotExist(err) {
		t.Errorf("stale icon.jpg should have been pruned when the ext changed to png")
	}
	if _, err := os.Stat(filepath.Join(imgDir, "icon.png")); err != nil {
		t.Errorf("icon.png should be present: %v", err)
	}
	got, _ := imagetree.Read(dir)
	if len(got["en-US"][images.Icon]) != 1 {
		t.Errorf("singular slot should read back exactly 1 image")
	}
}

// TestWrite_keepsStrayNonImageFiles asserts the per-slot prune only removes
// recognized image files, leaving a hand-added note intact (it is not a
// destructive RemoveAll of the slot directory).
func TestWrite_keepsStrayNonImageFiles(t *testing.T) {
	dir := t.TempDir()
	gallery := filepath.Join(dir, "en-US", "images", "phoneScreenshots")
	if err := os.MkdirAll(gallery, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gallery, "NOTES.txt"), []byte("hand notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := imagetree.Write(dir, imagetree.Tree{"en-US": {images.PhoneScreenshots: {pngBytes("a")}}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gallery, "NOTES.txt")); err != nil {
		t.Errorf("a stray non-image file must be preserved by the per-slot prune: %v", err)
	}
}

// TestExt_sniffsMagic asserts the extension is derived from the image bytes,
// not any header: PNG → png, JPEG → jpg, unknown → "".
func TestExt_sniffsMagic(t *testing.T) {
	if got := imagetree.Ext(pngBytes("x")); got != "png" {
		t.Errorf("PNG ext = %q, want png", got)
	}
	if got := imagetree.Ext(jpgBytes("x")); got != "jpg" {
		t.Errorf("JPEG ext = %q, want jpg", got)
	}
	if got := imagetree.Ext([]byte("GIF89a")); got != "" {
		t.Errorf("unknown ext = %q, want empty", got)
	}
}

// summarize renders a Tree as locale->type->len for readable failures.
func summarize(tr imagetree.Tree) map[string]map[string]int {
	out := map[string]map[string]int{}
	for loc, slots := range tr {
		out[loc] = map[string]int{}
		for ty, seq := range slots {
			out[loc][string(ty)] = len(seq)
		}
	}
	return out
}
