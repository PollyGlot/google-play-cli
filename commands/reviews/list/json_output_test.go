package list

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
)

const rawReview = `{"reviewId":"gp:AOqpTOE-example-1","authorName":"A Google user","comments":[{"userComment":{"text":"Crashes on start & loses my <streak>","lastModified":{"seconds":"1785748500"},"starRating":2,"reviewerLanguage":"en","appVersionCode":142,"appVersionName":"1.4.2"}},{"developerComment":{"text":"Fixed in 1.4.3","lastModified":{"seconds":"1785837600"}}}]}`

const rawReview2 = `{"reviewId":"gp:AOqpTOE-example-2","authorName":"A Google user","comments":[{"userComment":{"text":"Parfait","lastModified":{"seconds":"1785955200"},"starRating":5,"reviewerLanguage":"fr"}}]}`

func TestRenderJSON_reviews_golden(t *testing.T) {
	p := Payload{Reviews: []reviews.Review{
		{Raw: json.RawMessage(rawReview), ReviewID: "gp:AOqpTOE-example-1"},
		{Raw: json.RawMessage(rawReview2), ReviewID: "gp:AOqpTOE-example-2"},
	}}
	outputtest.GoldenJSON(t, "reviews.json.golden", p)
}

func TestRenderJSON_noReviews_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_reviews.json.golden", Payload{})
}
