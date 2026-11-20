package appstorecatalog_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestWire(t *testing.T) {
	ctx := context.Background()
	const store, pkg = "com.example.store", "com.example.app"
	const start, end = "2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z"
	sends := []testkit.Send{
		{Name: "GetRecentAppView", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appstorecatalog.GetRecentAppView(ctx, hc, store, pkg)
			return []any{v, raw}, err
		}},
		{Name: "ListRecentUpdateEvents", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appstorecatalog.ListRecentUpdateEvents(ctx, hc, store, start, end, 0, "")
			return []any{v, raw}, err
		}},
		{Name: "ListRecentUpdateEvents paged", Fn: func(hc *http.Client) (any, error) {
			v, raw, err := appstorecatalog.ListRecentUpdateEvents(ctx, hc, store, start, end, 500, "tok/+=")
			return []any{v, raw}, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
