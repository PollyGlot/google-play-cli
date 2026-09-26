package generatedapks_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	var got bytes.Buffer
	sends := []testkit.Send{
		{Name: "List", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := generatedapks.List(ctx, hc, pkg, 42)
			return []any{v, raw}, err
		}},
		{Name: "Download", Fn: func(hc *http.Client) (any, error) {
			got.Reset()
			n, err := generatedapks.Download(ctx, hc, pkg, 42, "dl/id+1", &got)
			if err == nil && n != int64(got.Len()) {
				return nil, fmt.Errorf("Download reported %d bytes, wrote %d", n, got.Len())
			}
			return got.String(), err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
