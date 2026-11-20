package testers_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/testers"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"googleGroups":["qa@example.com"]}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := testers.Get(ctx, hc, pkg, "e1", "beta"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := testers.Update(ctx, hc, pkg, "e1", "beta", []string{"qa@example.com", "b@example.com"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, _, err := testers.Update(ctx, hc, pkg, "e1", "beta", nil); err != nil {
		t.Fatalf("Update (clear): %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
