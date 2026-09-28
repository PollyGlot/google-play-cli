package artifacts_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/artifacts"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what every entry point of the package sends, byte for
// byte as a server reads it (testdata/requests.golden). The golden was
// recorded before the package moved onto the api executor (#585), so an
// unchanged file is the proof the migration kept every request identical.
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
