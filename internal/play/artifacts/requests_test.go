package artifacts_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/artifacts"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := artifacts.ListApks(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("ListApks: %v", err)
	}
	if _, _, err := artifacts.ListBundles(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
