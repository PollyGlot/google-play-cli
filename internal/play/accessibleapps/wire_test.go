package accessibleapps_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/accessibleapps"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
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
