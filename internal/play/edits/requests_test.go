package edits_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what every entry point of the package sends, byte for
// byte as a server reads it (testdata/requests.golden). The golden was
// recorded before the package moved onto the api executor (#585), so an
// unchanged file is the proof the migration kept every request identical.
func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, func(c testkit.Call) (int, string, bool) {
		if c.Method == http.MethodDelete {
			return http.StatusNoContent, "", true
		}
		return http.StatusOK, `{"id":"e1","expiryTimeSeconds":"1700000000"}`, true
	})
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if err := edits.WithEdit(ctx, hc, pkg, edits.Options{}, func(string) error { return nil }); err != nil {
		t.Fatalf("WithEdit: %v", err)
	}
	if err := edits.WithReadOnlyEdit(ctx, hc, pkg, func(string) error { return nil }); err != nil {
		t.Fatalf("WithReadOnlyEdit: %v", err)
	}
	if err := edits.Validate(ctx, hc, pkg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := edits.OpenExplicit(ctx, hc, pkg); err != nil {
		t.Fatalf("OpenExplicit: %v", err)
	}
	opts := edits.CommitOptions{ChangesInReview: edits.ChangesInReviewError, ChangesNotSentForReview: true}
	if err := edits.CommitExplicit(ctx, hc, pkg, "e1", opts); err != nil {
		t.Fatalf("CommitExplicit: %v", err)
	}
	if err := edits.DiscardExplicit(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("DiscardExplicit: %v", err)
	}
	if _, err := edits.ValidateExplicit(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("ValidateExplicit: %v", err)
	}
	if _, _, err := edits.GetExplicit(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("GetExplicit: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
