package images_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"images":[],"image":{"id":"i1","sha256":"abc"},"deleted":[]}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := images.List(ctx, hc, pkg, "e1", "en-US", images.PhoneScreenshots); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := images.Upload(ctx, hc, pkg, "e1", "en-US", images.Icon, testkit.PNG(2, 2), ""); err != nil {
		t.Fatalf("Upload png: %v", err)
	}
	if _, err := images.Upload(ctx, hc, pkg, "e1", "en-US", images.FeatureGraphic, testkit.JPEG(2, 2), ""); err != nil {
		t.Fatalf("Upload jpeg: %v", err)
	}
	if err := images.Delete(ctx, hc, pkg, "e1", "en-US", images.PhoneScreenshots, "img-7"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := images.DeleteAll(ctx, hc, pkg, "e1", "en-US", images.PhoneScreenshots); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
