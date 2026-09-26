package iap_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/iap"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire_listings pins the three paginated reads, which move onto the shared
// paginator (#586), byte for byte: testdata/wire.golden was recorded before
// the move and must not move with it.
func TestWire_listings(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	var b strings.Builder
	for _, l := range []struct {
		name, key, item, nextField string
		fn                         func(*http.Client) (any, error)
	}{
		{"ListOneTimeProducts", "oneTimeProducts", `{"productId":"coins"}`, "nextPageToken",
			func(hc *http.Client) (any, error) { v, err := iap.ListOneTimeProducts(ctx, hc, pkg); return v, err }},
		{"ListAllOffers", "oneTimeProductOffers", `{"productId":"coins","purchaseOptionId":"buy","offerId":"promo"}`, "nextPageToken",
			func(hc *http.Client) (any, error) { v, err := iap.ListAllOffers(ctx, hc, pkg); return v, err }},
		{"ListInAppProducts", "inappproduct", `{"sku":"coins"}`, "tokenPagination",
			func(hc *http.Client) (any, error) { v, err := iap.ListInAppProducts(ctx, hc, pkg); return v, err }},
	} {
		page := func(next string) string {
			tok := `"nextPageToken":"` + next + `"`
			if l.nextField == "tokenPagination" {
				tok = `"tokenPagination":{` + tok + `}`
			}
			return `{"` + l.key + `":[` + l.item + `],` + tok + `}`
		}
		send := []testkit.Send{{Name: l.name, Fn: l.fn}}
		b.WriteString(testkit.Exchanges(send, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))
		b.WriteString(testkit.Exchanges(send, testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(page("p/2+"), page(""))}}))
		b.WriteString(testkit.Exchanges(send, testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(page("again"))}}))
		b.WriteString(testkit.Exchanges(send, testkit.Answer{Name: "anonymous item", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{"`+l.key+`":[{}]}`)}}))
	}
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
