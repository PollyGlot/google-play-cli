package sharing_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/sharing"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	artifact := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(artifact, []byte("PK\x03\x04 artifact bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sends := []testkit.Send{
		{Name: "UploadAPK", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := sharing.UploadAPK(ctx, hc, pkg, artifact)
			return []any{v, raw}, err
		}},
		{Name: "UploadBundle", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := sharing.UploadBundle(ctx, hc, pkg, artifact)
			return []any{v, raw}, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
