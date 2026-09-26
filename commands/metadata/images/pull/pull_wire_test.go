package imagespull

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire_download pins the image-bytes download, which moved onto the
// executor's streaming download (#586), byte for byte: testdata/wire.golden
// was recorded on the hand-rolled request and must not move with it.
func TestWire_download(t *testing.T) {
	const u = "https://play-lh.googleusercontent.com/abc=w512-h512"
	send := []testkit.Send{{Name: "download", Fn: func(hc *http.Client) (any, error) {
		b, err := download(context.Background(), hc, u)
		return b, err
	}}}
	golden := testkit.Exchanges(send,
		testkit.Answer{Name: "ok", Responders: []testkit.Responder{testkit.Any(http.StatusOK, "\x89PNG image bytes")}},
		testkit.Answer{Name: "not found", Responders: []testkit.Responder{testkit.Any(http.StatusNotFound, "<html>404</html>")}},
		testkit.AnswerDenied,
		testkit.Answer{Name: "over the cap", Responders: []testkit.Responder{testkit.Any(http.StatusOK, strings.Repeat("x", maxImageBytes+1))}},
	)
	testkit.Golden(t, "wire.golden", []byte(golden))
}
