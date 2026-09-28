package listings_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/listings"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what every entry point of the package sends, byte for
// byte as a server reads it (testdata/requests.golden). The golden was
// recorded before the package moved onto the api executor (#585), so an
// unchanged file is the proof the migration kept every request identical.
func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"language":"en-US","title":"T","listings":[]}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := listings.List(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, _, err := listings.Get(ctx, hc, pkg, "e1", "fr-FR"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := listings.Patch(ctx, hc, pkg, "e1", "fr-FR", []byte(`{"title":"Été"}`)); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if _, err := listings.Update(ctx, hc, pkg, "e1", "de-DE", []byte(`{"language":"de-DE","title":"T"}`)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := listings.Delete(ctx, hc, pkg, "e1", "de-DE"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
