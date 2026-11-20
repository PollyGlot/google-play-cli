package details_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t,
		func(c testkit.Call) (int, string, bool) {
			return 200, `{"id":"e1"}`, c.Method == "POST"
		},
		func(c testkit.Call) (int, string, bool) {
			return 204, "", c.Method == "DELETE"
		},
		testkit.Any(200, `{"defaultLanguage":"en-US","contactEmail":"a@example.com","title":"T","images":[{"url":"u","sha256":"s"}]}`),
	)
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := details.GetDetails(ctx, hc, pkg); err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if _, _, err := details.Get(ctx, hc, pkg); err != nil {
		t.Fatalf("Get: %v", err)
	}
	email, empty := "b@example.com", ""
	if _, _, err := details.Patch(ctx, hc, pkg, "e1", details.AppDetailsPatch{ContactEmail: &email, ContactWebsite: &empty}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if _, err := details.GetDefaultLanguage(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("GetDefaultLanguage: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
