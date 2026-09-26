package datasafety_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	sends := []testkit.Send{
		{Name: "Post", Fn: func(hc *http.Client) (any, error) {
			v, err := datasafety.Post(ctx, hc, "com.example.app", []byte("Question ID,Response\n\"PSL_DATA\",<true>\n"))
			return v, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
