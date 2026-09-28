package appsigning_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const name = "applications/com.example.app"
	const kms = "projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"
	sends := []testkit.Send{
		{Name: "Enroll existing", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appsigning.Enroll(ctx, hc, name, appsigning.EnrollOpts{KmsKeyResource: kms})
			return []any{v, raw}, err
		}},
		{Name: "Enroll new", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appsigning.Enroll(ctx, hc, name, appsigning.EnrollOpts{
				KmsKeyResource: kms, NewApp: true, PemCertificate: []byte("CERT"), PemUploadCertificate: []byte("UPLOAD"),
			})
			return []any{v, raw}, err
		}},
		{Name: "Rotate", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appsigning.Rotate(ctx, hc, name, appsigning.RotateOpts{
				KmsKeyResource: kms, PemCertificate: []byte("CERT"), Lineage: []byte("LINEAGE"), Reason: "ROUTINE_KEY_UPGRADE",
			})
			return []any{v, raw}, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
