package recovery_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestWire pins every request this package sends and how each answer maps to
// a result or an error, byte for byte: testdata/wire.golden was recorded
// before the move onto the executor (#586) and must not move with it.
func TestWire(t *testing.T) {
	ctx := context.Background()
	const pkg = "com.example.app"
	sends := []testkit.Send{
		{Name: "Create", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := recovery.Create(ctx, hc, pkg, recovery.CreateOpts{
				VersionCodes: []int64{41, 42}, Regions: []string{"FR"}, SdkLevels: []int64{33}, RemoteInAppUpdate: true,
			})
			return []any{v, raw}, err
		}},
		{Name: "Create all users", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := recovery.Create(ctx, hc, pkg, recovery.CreateOpts{VersionCodes: []int64{42}, AllUsers: true})
			return []any{v, raw}, err
		}},
		{Name: "List", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := recovery.List(ctx, hc, pkg, 42)
			return []any{v, raw}, err
		}},
		{Name: "Deploy", Fn: func(hc *http.Client) (any, error) { v, err := recovery.Deploy(ctx, hc, pkg, "7"); return v, err }},
		{Name: "Cancel", Fn: func(hc *http.Client) (any, error) { v, err := recovery.Cancel(ctx, hc, pkg, "7"); return v, err }},
		{Name: "AddTargeting", Fn: func(hc *http.Client) (any, error) {
			v, err := recovery.AddTargeting(ctx, hc, pkg, "7", recovery.BuildTargeting(false, []string{"DE"}, nil))
			return v, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
