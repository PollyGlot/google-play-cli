package datasafety_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

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
