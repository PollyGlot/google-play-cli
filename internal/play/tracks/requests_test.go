package tracks_test

import (
	"context"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRequests_wire pins what every entry point of the package sends, byte for
// byte as a server reads it (testdata/requests.golden). The golden was
// recorded before the package moved onto the api executor (#585), so an
// unchanged file is the proof the migration kept every request identical.
func TestRequests_wire(t *testing.T) {
	w := testkit.NewWire(t, testkit.Any(200, `{"track":"beta","releases":[],"tracks":[]}`))
	ctx, hc, pkg := context.Background(), w.Client(), "com.example.app"

	if _, _, err := tracks.Get(ctx, hc, pkg, "e1", "beta"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := tracks.List(ctx, hc, pkg, "e1"); err != nil {
		t.Fatalf("List: %v", err)
	}
	release := tracks.Release{
		Name: "142", Status: "inProgress", UserFraction: 0.1, VersionCodes: []string{"142"},
		ReleaseNotes: []tracks.LocalizedText{{Language: "en-US", Text: "Fixes <&> \"quotes\""}},
	}
	if _, _, err := tracks.Update(ctx, hc, pkg, "e1", "beta", release); err != nil {
		t.Fatalf("Update: %v", err)
	}
	raw := []byte(`{"track":"beta","releases":[{"status":"halted","countryTargeting":{"countries":["FR"]}}]}`)
	if _, _, err := tracks.UpdateRaw(ctx, hc, pkg, "e1", "beta", raw); err != nil {
		t.Fatalf("UpdateRaw: %v", err)
	}
	if _, _, err := tracks.Create(ctx, hc, pkg, "e1", "qa team", tracks.FormFactorDefault); err != nil {
		t.Fatalf("Create: %v", err)
	}
	testkit.Golden(t, "requests.golden", w.Transcript())
}
