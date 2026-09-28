package orders_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/orders"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	sends := []testkit.Send{
		{Name: "Get", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := orders.Get(ctx, hc, pkg, "GPA.1")
			return []any{v, raw}, err
		}},
		{Name: "BatchGet", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := orders.BatchGet(ctx, hc, pkg, []string{"GPA.1", "GPA.2"})
			return []any{v, raw}, err
		}},
		{Name: "Refund", Fn: func(hc *http.Client) (any, error) {
			v, err := orders.Refund(ctx, hc, pkg, "GPA.1", false)
			return v, err
		}},
		{Name: "Refund revoke", Fn: func(hc *http.Client) (any, error) {
			v, err := orders.Refund(ctx, hc, pkg, "GPA.1", true)
			return v, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
