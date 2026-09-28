package reviews_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	list := func(hc *http.Client) (any, error) { v, err := reviews.List(ctx, hc, pkg); return v, err }
	sends := []testkit.Send{
		{Name: "List", Fn: list},
		{Name: "Get", Fn: func(hc *http.Client) (any, error) { v, err := reviews.Get(ctx, hc, pkg, "gp:AOqp/x+1"); return v, err }},
		{Name: "Reply", Fn: func(hc *http.Client) (any, error) {
			v, err := reviews.Reply(ctx, hc, pkg, "gp:AOqp/x+1", "Thanks <3 & \"quotes\"")
			return v, err
		}},
	}
	var b strings.Builder
	b.WriteString(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed))

	page := func(id, next string) string {
		return `{"reviews":[{"reviewId":"` + id + `"}],"tokenPagination":{"nextPageToken":"` + next + `"}}`
	}
	one := []testkit.Send{{Name: "List", Fn: list}}
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "two pages", Responders: []testkit.Responder{testkit.Sequence(page("r1", "p/2+ &x"), page("r2", ""))}}))
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "token loop", Responders: []testkit.Responder{testkit.Sequence(page("r1", "again"))}}))
	b.WriteString(testkit.Exchanges(one, testkit.Answer{Name: "bad review", Responders: []testkit.Responder{testkit.Any(http.StatusOK, `{"reviews":[7]}`)}}))
	testkit.Golden(t, "wire.golden", []byte(b.String()))
}
