package appstore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func TestWire(t *testing.T) {
	ctx := context.Background()
	const store, pkg = "com.example.store", "com.example.app"
	sends := []testkit.Send{
		{Name: "CreateHostedApp", Fn: func(hc *http.Client) (any, error) {
			v, err := appstore.CreateHostedApp(ctx, hc, store, pkg)
			return v, err
		}},
		{Name: "UpdateHostedApp", Fn: func(hc *http.Client) (any, error) {
			v, err := appstore.UpdateHostedApp(ctx, hc, store, pkg, json.RawMessage(`{"packageName":"other","developerName":"Ex","zeta":1,"alpha":[1,2]}`))
			return v, err
		}},
		{Name: "UpdatePublishStatus", Fn: func(hc *http.Client) (any, error) {
			v, err := appstore.UpdatePublishStatus(ctx, hc, store, pkg, appstore.PublishStateUnpublished)
			return v, err
		}},
	}
	testkit.Golden(t, "wire.golden", []byte(testkit.Exchanges(sends, testkit.AnswerOK, testkit.AnswerDenied, testkit.AnswerMalformed)))
}
