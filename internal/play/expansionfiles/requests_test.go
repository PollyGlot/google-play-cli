package expansionfiles_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"referencesVersion":7}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, err := expansionfiles.Update(ctx, hc, pkg, "e1", 42, expansionfiles.TypePatch, 7); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, _, err := expansionfiles.Get(ctx, hc, pkg, "e1", 42, expansionfiles.TypeMain); err != nil {
		t.Fatalf("Get: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
