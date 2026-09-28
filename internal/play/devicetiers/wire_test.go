package devicetiers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	body := []byte(`{"deviceGroups":[{"name":"high"}]}`)
	sends := []testkit.Send{
		{Name: "Create", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := devicetiers.Create(ctx, hc, pkg, body, false)
			return []any{v, raw}, err
		}},
		{Name: "Create allow unknown", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := devicetiers.Create(ctx, hc, pkg, body, true)
			return []any{v, raw}, err
		}},
		{Name: "Get", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := devicetiers.Get(ctx, hc, pkg, "123")
			return []any{v, raw}, err
		}},
		{Name: "List", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := devicetiers.List(ctx, hc, pkg, 0, "")
			return []any{v, raw}, err
		}},
		{Name: "List paged", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := devicetiers.List(ctx, hc, pkg, 10, "tok/+=")
			return []any{v, raw}, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
