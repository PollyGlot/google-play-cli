package systemapks_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte.
func TestWire(t *testing.T) {
	ctx := context.Background()
	var got bytes.Buffer
	spec := systemapks.Variant{DeviceSpec: &systemapks.DeviceSpec{ScreenDensity: 480, SupportedAbis: []string{"arm64-v8a"}}}
	sends := []testkit.Send{
		{Name: "Create", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := systemapks.Create(ctx, hc, pkg, 42, spec)
			return []any{v, raw}, err
		}},
		{Name: "List", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := systemapks.List(ctx, hc, pkg, 42)
			return []any{v, raw}, err
		}},
		{Name: "Get", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := systemapks.Get(ctx, hc, pkg, 42, 7)
			return []any{v, raw}, err
		}},
		{Name: "Download", Fn: func(hc *http.Client) (any, error) {
			got.Reset()
			n, err := systemapks.Download(ctx, hc, pkg, 42, 7, &got)
			if err == nil && n != int64(got.Len()) {
				return nil, fmt.Errorf("Download reported %d bytes, wrote %d", n, got.Len())
			}
			return got.String(), err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
