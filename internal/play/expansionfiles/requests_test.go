package expansionfiles_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what the executor-backed entry points of the package
// send, byte for byte as a server reads it (testdata/requests.golden). The
// golden was recorded before the package moved onto the api executor (#585),
// so an unchanged file is the proof the migration kept every request
// identical. Upload is not here: it already went through the shared
// api.ResumableUpload and the migration does not touch it.
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
