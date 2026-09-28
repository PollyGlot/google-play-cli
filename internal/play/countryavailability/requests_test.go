package countryavailability_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what the package sends, byte for byte as a server
// reads it (testdata/requests.golden). The golden was recorded before the
// package moved onto the api executor (#585), so an unchanged file is the
// proof the migration kept the request identical.
func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"countries":[]}`))
	if _, _, err := countryavailability.Get(context.Background(), w.Client(), "com.example.app", "e1", "closed testing"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
