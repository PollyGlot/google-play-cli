package pull

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestRenderJSON_pulled_golden(t *testing.T) {
	en := listing.NewListing("en-US")
	en.Set(listing.Video, "https://www.youtube.com/watch?v=example")
	en.Set(listing.Title, "Example App")
	en.Set(listing.FullDescription, "Track habits <fast> & free.")
	en.Set(listing.ShortDescription, "Track habits")
	fr := listing.NewListing("fr-FR")
	fr.Set(listing.Title, "Mon Appli")
	tr := listing.Tree{"fr-FR": fr, "en-US": en}

	outputtest.GoldenJSON(t, "pulled.json.golden", newPayload("com.example.app", "./metadata", tr))
}

func TestRenderJSON_noListings_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_listings.json.golden", newPayload("com.example.app", "./metadata", listing.Tree{}))
}
