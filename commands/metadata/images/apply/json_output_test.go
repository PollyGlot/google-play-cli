package imagesapply_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	imagesapply "github.com/PollyGlot/google-play-cli/commands/metadata/images/apply"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// blob and live stand in for a local image file and its live twin: the diff
// matches on sha256, so equal bytes are the same image.
func blob(name string) []byte { return []byte("png:" + name) }

func live(id, name string) images.Image {
	sum := sha256.Sum256(blob(name))
	return images.Image{ID: id, Sha256: hex.EncodeToString(sum[:])}
}

// slots yields every ADR-0013 op through the real diff engine: a singular
// replace, a gallery append, an unchanged slot, a reorder, an untouched slot,
// and a gallery holding one online-only image (kept additively, deleted under
// --prune).
func slots() []imagediff.SlotInput {
	return []imagediff.SlotInput{
		{Locale: "de-DE", Type: images.PhoneScreenshots, Local: [][]byte{blob("b"), blob("a")}, Live: []images.Image{live("1", "a"), live("2", "b")}},
		{Locale: "en-US", Type: images.Icon, Local: [][]byte{blob("icon-v2")}, Live: []images.Image{live("3", "icon-v1")}},
		{Locale: "en-US", Type: images.FeatureGraphic, Local: [][]byte{blob("feature")}, Live: []images.Image{live("4", "feature")}},
		{Locale: "en-US", Type: images.PhoneScreenshots, Local: [][]byte{blob("s1"), blob("s2"), blob("s3")}, Live: []images.Image{live("5", "s1"), live("6", "s2")}},
		{Locale: "en-US", Type: images.SevenInchScreenshots, Local: [][]byte{blob("t1")}, Live: []images.Image{live("7", "t1"), live("8", "t-old")}},
		{Locale: "fr-FR", Type: images.PhoneScreenshots, Live: []images.Image{live("9", "fr1")}},
	}
}

func payload(prune, dryRun bool) imagesapply.Payload {
	diff := imagediff.Aggregate("com.example.app", imagediff.Plans(slots(), prune))
	return imagesapply.Payload{Result: &imageorchestrator.Result{Package: "com.example.app", DryRun: dryRun, Diff: diff}}
}

// TestRenderJSON_dryRun_golden freezes the ADR-0013 §5 diff schema under the
// additive default: position -1 marks slot-level records, and the online-only
// seven-inch screenshot leaves its slot "unchanged".
func TestRenderJSON_dryRun_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run.json.golden", payload(false, true))
}

// TestRenderJSON_dryRunPrune_golden freezes --prune: the same online-only
// image now surfaces as a delete record carrying its sha256.
func TestRenderJSON_dryRunPrune_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run_prune.json.golden", payload(true, true))
}

// TestRenderJSON_applied_matchesDryRun pins that a real apply prints the very
// diff it executed: the JSON renderer ignores DryRun, so a script reads one
// schema whichever mode ran.
func TestRenderJSON_applied_matchesDryRun(t *testing.T) {
	got := string(outputtest.RenderJSON(t, payload(false, false)))
	want := string(outputtest.RenderJSON(t, payload(false, true)))
	if got != want {
		t.Errorf("real-apply JSON differs from the dry-run diff\n got: %s\nwant: %s", got, want)
	}
}
