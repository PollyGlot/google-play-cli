package gcs_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire_listObjects pins the paginated listing, which moves onto the
// shared paginator (#586), byte for byte: testdata/wire.golden was recorded
// before the move and changes only where the copies disagreed (the loop
// error names nextPageToken, the field the server repeated, like the others).
func TestWire_listObjects(t *testing.T) {
	ctx := context.Background()
	list := []testkit.Send{{Name: "ListObjects", Fn: func(hc *http.Client) (any, error) {
		v, err := gcs.ListObjects(ctx, hc, "pubsite_prod_rev_01234", "reviews/reviews_com.example.app")
		return v, err
	}}}
	page := func(obj, next string) string {
		return `{"items":[{"name":"` + obj + `"}],"nextPageToken":"` + next + `"}`
	}
	var b strings.Builder
	b.WriteString(testkit.Exchanges(list, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))
	b.WriteString(testkit.Exchanges(list, testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(page("a.csv", "p/2+"), page("b.csv", ""))}}))
	b.WriteString(testkit.Exchanges(list, testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(page("a.csv", "again"))}}))
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
