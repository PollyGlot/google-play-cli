package countryavailability_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/countryavailability"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"countries":[]}`))
	if _, _, err := countryavailability.Get(context.Background(), w.Client(), "com.example.app", "e1", "closed testing"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
