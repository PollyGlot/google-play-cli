package accessibleapps_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestWire(t *testing.T) {
	ctx := context.Background()
	sends := []testkit.Send{
		{Name: "Search", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := accessibleapps.Search(ctx, hc, 0, "")
			return []any{v, raw}, err
		}},
		{Name: "Search paged", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := accessibleapps.Search(ctx, hc, 50, "tok/+=")
			return []any{v, raw}, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
