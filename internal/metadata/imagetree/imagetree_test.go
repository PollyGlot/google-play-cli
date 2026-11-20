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

func pngBytes(tag string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), tag...) }
func jpgBytes(tag string) []byte { return append([]byte("\xff\xd8\xff\xe0"), tag...) }

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

func TestRead_galleryOrderIsFilenameSort(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "en-US", "images", "phoneScreenshots")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestRead_emptyAndStrayAreHarmless(t *testing.T) {
	dir := t.TempDir()
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

func TestRead_retiredSlotIsReportedNotRead(t *testing.T) {
	dir := t.TempDir()
	imgDir := filepath.Join(dir, "en-US", "images")
	if err := os.MkdirAll(filepath.Join(dir, "fr-FR", "images", "promoGraphic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"promoGraphic.png": pngBytes("promo"),
		"promoGraphic.txt": []byte("not an image"),
		"icon.png":         pngBytes("icon"),
	} {
		if err := os.WriteFile(filepath.Join(imgDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tr, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := summarize(tr); !reflect.DeepEqual(got, map[string]map[string]int{"en-US": {"icon": 1}}) {
		t.Errorf("Read = %v, want only the en-US icon", got)
	}

	got, err := imagetree.Retired(dir)
	if err != nil {
		t.Fatalf("Retired: %v", err)
	}
	want := []string{
		filepath.Join(dir, "en-US", "images", "promoGraphic.png"),
		filepath.Join(dir, "fr-FR", "images", "promoGraphic"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Retired = %v, want %v", got, want)
	}
}

func TestRetired_absentDir_isEmptyNotError(t *testing.T) {
	got, err := imagetree.Retired(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(got) != 0 {
		t.Errorf("Retired(absent) = %v, %v; want none, nil", got, err)
	}
}

func TestWriteStaged_matchesWrite(t *testing.T) {
	tr := imagetree.Tree{
		"en-US": {
			images.Icon:             {pngBytes("icon")},
			images.PhoneScreenshots: {pngBytes("a"), jpgBytes("b")},
		},
		"fr-FR": {images.FeatureGraphic: {jpgBytes("fr-feat")}},
	}
	staging := t.TempDir()
	st := make(imagetree.Staged)
	n := 0
	for loc, slots := range tr {
		st[loc] = make(map[images.Type][]string)
		for ty, seq := range slots {
			for _, data := range seq {
				p := filepath.Join(staging, string(rune('a'+n)))
				n++
				if err := os.WriteFile(p, data, 0o600); err != nil {
					t.Fatal(err)
				}
				st[loc][ty] = append(st[loc][ty], p)
			}
		}
	}
	dir := t.TempDir()
	if err := imagetree.WriteStaged(dir, st); err != nil {
		t.Fatalf("WriteStaged: %v", err)
	}
	got, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !reflect.DeepEqual(got, tr) {
		t.Errorf("WriteStaged mismatch:\n got = %v\nwant = %v", summarize(got), summarize(tr))
	}
	if left, _ := os.ReadDir(staging); len(left) != n {
		t.Errorf("staged files left = %d, want %d (WriteStaged must not move them)", len(left), n)
	}
}

func TestWriteStaged_rejectsNonImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(p, []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := imagetree.WriteStaged(t.TempDir(), imagetree.Staged{"en-US": {images.Icon: {p}}}); err == nil {
		t.Fatal("WriteStaged accepted a non-image file")
	}
}
