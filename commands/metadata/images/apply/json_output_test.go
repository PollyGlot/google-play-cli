package imagesapply_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	imagesapply "github.com/PollyGlot/google-play-cli/commands/metadata/images/apply"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

func blob(name string) []byte { return []byte("png:" + name) }

func shas(names ...[]byte) []string {
	out := make([]string, len(names))
	for i, b := range names {
		sum := sha256.Sum256(b)
		out[i] = hex.EncodeToString(sum[:])
	}
	return out
}

func live(id, name string) images.Image {
	sum := sha256.Sum256(blob(name))
	return images.Image{ID: id, Sha256: hex.EncodeToString(sum[:])}
}

func slots() []imagediff.SlotInput {
	return []imagediff.SlotInput{
		{Locale: "de-DE", Type: images.PhoneScreenshots, Local: shas(blob("b"), blob("a")), Live: []images.Image{live("1", "a"), live("2", "b")}},
		{Locale: "en-US", Type: images.Icon, Local: shas(blob("icon-v2")), Live: []images.Image{live("3", "icon-v1")}},
		{Locale: "en-US", Type: images.FeatureGraphic, Local: shas(blob("feature")), Live: []images.Image{live("4", "feature")}},
		{Locale: "en-US", Type: images.PhoneScreenshots, Local: shas(blob("s1"), blob("s2"), blob("s3")), Live: []images.Image{live("5", "s1"), live("6", "s2")}},
		{Locale: "en-US", Type: images.SevenInchScreenshots, Local: shas(blob("t1")), Live: []images.Image{live("7", "t1"), live("8", "t-old")}},
		{Locale: "fr-FR", Type: images.PhoneScreenshots, Live: []images.Image{live("9", "fr1")}},
	}
}

func payload(prune, dryRun bool) imagesapply.Payload {
	diff := imagediff.Aggregate("com.example.app", imagediff.Plans(slots(), prune))
	return imagesapply.Payload{Result: &imageorchestrator.Result{Package: "com.example.app", DryRun: dryRun, Diff: diff}}
}

func TestRenderJSON_dryRun_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run.json.golden", payload(false, true))
}

func TestRenderJSON_dryRunPrune_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "dry_run_prune.json.golden", payload(true, true))
}

func TestRenderJSON_applied_matchesDryRun(t *testing.T) {
	got := string(outputtest.RenderJSON(t, payload(false, false)))
	preview := string(outputtest.RenderJSON(t, payload(false, true)))
	want := strings.Replace(preview, "\n  \"dryRun\": true,", "", 1)
	if want == preview {
		t.Fatalf("dry-run JSON lacks the leading dryRun marker:\n%s", preview)
	}
	if got != want {
		t.Errorf("real-apply JSON differs from the dry-run diff\n got: %s\nwant: %s", got, want)
	}
}
