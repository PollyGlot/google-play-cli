package history

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/reviews/history"
)

func TestRenderJSON_rows_golden(t *testing.T) {
	p := Payload{Rows: []history.Row{
		{
			PackageName:                      "com.example.app",
			AppVersionCode:                   "142",
			AppVersionName:                   "1.4.2",
			ReviewerLanguage:                 "en",
			Device:                           "a52q",
			ReviewSubmitDateAndTime:          "2026-08-03T09:15:00Z",
			ReviewSubmitMillisSinceEpoch:     "1785748500000",
			ReviewLastUpdateDateAndTime:      "2026-08-03T09:15:00Z",
			ReviewLastUpdateMillisSinceEpoch: "1785748500000",
			StarRating:                       "2",
			ReviewTitle:                      "Sync <broken>",
			ReviewText:                       "Crashes on start & loses my streak",
			DeveloperReplyDateAndTime:        "2026-08-04T10:00:00Z",
			DeveloperReplyMillisSinceEpoch:   "1785837600000",
			DeveloperReplyText:               "Fixed in 1.4.3, thanks for the report!",
			ReviewLink:                       "https://play.google.com/apps/publish?account=0&review=gp:AOqpTOE-example-1",
		},
		{
			PackageName:                      "com.example.app",
			AppVersionCode:                   "142",
			AppVersionName:                   "1.4.2",
			ReviewerLanguage:                 "fr",
			Device:                           "oriole",
			ReviewSubmitDateAndTime:          "2026-08-05T18:40:00Z",
			ReviewSubmitMillisSinceEpoch:     "1785955200000",
			ReviewLastUpdateDateAndTime:      "2026-08-05T18:40:00Z",
			ReviewLastUpdateMillisSinceEpoch: "1785955200000",
			StarRating:                       "5",
			ReviewText:                       "Parfait",
			ReviewLink:                       "https://play.google.com/apps/publish?account=0&review=gp:AOqpTOE-example-2",
		},
	}}
	outputtest.GoldenJSON(t, "rows.json.golden", p)
}

func TestRenderJSON_noRows_golden(t *testing.T) {
	outputtest.GoldenJSON(t, "no_rows.json.golden", Payload{})
}
