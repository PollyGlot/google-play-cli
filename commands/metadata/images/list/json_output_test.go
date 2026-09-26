package imageslist_test

import (
	"encoding/json"
	"testing"

	imageslist "github.com/PollyGlot/google-play-cli/commands/metadata/images/list"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// rawIcon is an edits.images.list images array as Play shapes it, with an &
// in the URL: the gplay envelope must carry it unescaped and verbatim.
const rawIcon = `[{"id":"img-icon-1","url":"https://play-lh.googleusercontent.com/icon?w=512&h=512","sha1":"da39a3ee5e6b4b0d3255bfef95601890afd80709","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}]`

// TestRenderJSON_slots_golden freezes the gplay envelope around the verbatim
// images arrays (ADR-0003): package, then one entry per non-empty slot with
// its locale, type and count. The second slot lost its raw bytes, so it pins
// the re-marshal fallback, which keeps the API field names.
func TestRenderJSON_slots_golden(t *testing.T) {
	p := imageslist.Payload{
		Package: "com.example.app",
		Slots: []imageslist.Slot{
			{
				Locale:    "en-US",
				Type:      images.Icon,
				Images:    []images.Image{{ID: "img-icon-1", Sha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}},
				RawImages: json.RawMessage(rawIcon),
			},
			{
				Locale: "en-US",
				Type:   images.PhoneScreenshots,
				Images: []images.Image{
					{ID: "img-shot-1", URL: "https://play-lh.googleusercontent.com/shot1", Sha1: "356a192b7913b04c54574d18c28d46e6395428ab", Sha256: "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"},
					{ID: "img-shot-2", URL: "https://play-lh.googleusercontent.com/shot2", Sha1: "da4b9237bacccdf19c0760cab7aec4a8359010b0", Sha256: "d4735e3a265e16eee03f59718b9b5d03019c07d8b6c51f90da3a666eec13ab35"},
				},
			},
		},
	}
	outputtest.GoldenJSON(t, "slots.json.golden", p)
}

// TestRenderJSON_noImages_golden pins an app with no image to "slots": []
// (never null), so a consumer can iterate without a guard.
func TestRenderJSON_noImages_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_images.json.golden", imageslist.Payload{Package: "com.example.app"})
}
