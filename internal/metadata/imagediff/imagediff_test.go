package imagediff_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func img(id string, b []byte) images.Image { return images.Image{ID: id, Sha256: sha(b)} }

func B(tag string) []byte { return []byte("png-" + tag) }

func local(bs ...[]byte) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = sha(b)
	}
	return out
}

func opsOf(changes []imagediff.SlotChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, string(c.Op))
	}
	return out
}

func hasOp(changes []imagediff.SlotChange, op imagediff.Op) bool {
	for _, c := range changes {
		if c.Op == op {
			return true
		}
	}
	return false
}

func TestUnchanged_isNoOp(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: local(B("a"), B("b")),
		Live:  []images.Image{img("1", B("a")), img("2", B("b"))},
	}
	plans := imagediff.Plans([]imagediff.SlotInput{in}, false)
	if plans[0].Kind != imagediff.KindNone {
		t.Errorf("identical slot should be KindNone, got %v", plans[0].Kind)
	}
	if !hasOp(plans[0].Changes, imagediff.OpUnchanged) {
		t.Errorf("identical slot should be unchanged, got %v", opsOf(plans[0].Changes))
	}
}

func TestSingular_upload_whenLiveEmpty(t *testing.T) {
	in := imagediff.SlotInput{Locale: "en-US", Type: images.Icon, Local: local(B("icon"))}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if p.Kind != imagediff.KindRewrite || len(p.Uploads) != 1 {
		t.Errorf("singular upload should rewrite with 1 upload, got kind=%v uploads=%d", p.Kind, len(p.Uploads))
	}
	if !hasOp(p.Changes, imagediff.OpUpload) {
		t.Errorf("singular upload should emit an upload op, got %v", opsOf(p.Changes))
	}
}

func TestSingular_replace_whenContentDiffers(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.Icon,
		Local: local(B("new-icon")),
		Live:  []images.Image{img("old", B("old-icon"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if p.Kind != imagediff.KindRewrite {
		t.Errorf("singular replace should be KindRewrite, got %v", p.Kind)
	}
	if !hasOp(p.Changes, imagediff.OpUpload) {
		t.Errorf("singular replace should emit upload, got %v", opsOf(p.Changes))
	}
}

func TestGallery_pureAppend_uploadsTail(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: local(B("a"), B("b"), B("c")),
		Live:  []images.Image{img("1", B("a")), img("2", B("b"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if p.Kind != imagediff.KindAppend {
		t.Errorf("pure append should be KindAppend, got %v", p.Kind)
	}
	if len(p.Uploads) != 1 {
		t.Errorf("pure append should upload only the tail (1), got %d", len(p.Uploads))
	}
	if hasOp(p.Changes, imagediff.OpReorder) {
		t.Errorf("pure append must not reorder: %v", opsOf(p.Changes))
	}
}

func TestGallery_reorder_whenOrderDiffers_noLoss(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: local(B("b"), B("a")),
		Live:  []images.Image{img("1", B("a")), img("2", B("b"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if p.Kind != imagediff.KindRewrite {
		t.Errorf("reorder should be KindRewrite, got %v", p.Kind)
	}
	if !hasOp(p.Changes, imagediff.OpReorder) {
		t.Errorf("reorder should emit a reorder op, got %v", opsOf(p.Changes))
	}
	if len(p.Uploads) != 2 {
		t.Errorf("rewrite should re-upload the whole local seq (2), got %d", len(p.Uploads))
	}
}

func TestGallery_additive_keepsOnlineOnly(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.PhoneScreenshots,
		Local: local(B("a"), B("new")),
		Live:  []images.Image{img("1", B("a")), img("2", B("extra"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if hasOp(p.Changes, imagediff.OpDelete) {
		t.Errorf("additive must never emit a delete: %v", opsOf(p.Changes))
	}
	if hasOp(p.Changes, imagediff.OpReorder) {
		t.Errorf("additive with online-only present must not reorder (would drop it): %v", opsOf(p.Changes))
	}
	if p.Kind != imagediff.KindAppend || len(p.Uploads) != 1 {
		t.Errorf("additive should append only the new image, got kind=%v uploads=%d", p.Kind, len(p.Uploads))
	}
}

func TestUntouchedSlot_whenLocalEmpty(t *testing.T) {
	in := imagediff.SlotInput{
		Locale: "en-US", Type: images.Icon,
		Live: []images.Image{img("old", B("icon"))},
	}
	p := imagediff.Plans([]imagediff.SlotInput{in}, false)[0]
	if p.Kind != imagediff.KindNone {
		t.Errorf("unmanaged slot should be KindNone, got %v", p.Kind)
	}
	if !hasOp(p.Changes, imagediff.OpUntouchedSlot) {
		t.Errorf("unmanaged-but-live slot should be untouchedSlot, got %v", opsOf(p.Changes))
	}
}

func TestAggregate_summaryAndSchema(t *testing.T) {
	slots := []imagediff.SlotInput{
		{Locale: "en-US", Type: images.Icon, Local: local(B("icon"))},
		{Locale: "en-US", Type: images.PhoneScreenshots, Local: local(B("b"), B("a")), Live: []images.Image{img("1", B("a")), img("2", B("b"))}},
	}
	res := imagediff.Aggregate("com.example.app", imagediff.Plans(slots, false))
	if res.Package != "com.example.app" {
		t.Errorf("package = %q", res.Package)
	}
	if res.Summary.Upload < 1 || res.Summary.Reorder < 1 {
		t.Errorf("summary should count upload + reorder: %+v", res.Summary)
	}
	if !res.HasChanges() {
		t.Errorf("HasChanges should be true when upload+delete+reorder > 0")
	}
	b, _ := json.Marshal(res)
	var doc struct {
		Package string `json:"package"`
		Slots   []struct {
			Locale    string `json:"locale"`
			ImageType string `json:"imageType"`
			Op        string `json:"op"`
			Position  int    `json:"position"`
		} `json:"slots"`
		Summary struct {
			Upload, Delete, Reorder, Unchanged int
		} `json:"summary"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("Result JSON not the ADR-0013 schema: %v\n%s", err, b)
	}
	if len(doc.Slots) == 0 {
		t.Errorf("slots should be populated: %s", b)
	}
}
