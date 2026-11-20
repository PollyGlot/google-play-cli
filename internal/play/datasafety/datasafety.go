package datasafety

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opDataSafety = "applications.dataSafety"

var method = apiregistry.MustResolve("androidpublisher.applications.dataSafety")

type request struct {
	SafetyLabels string `json:"safetyLabels"`
}

func Post(ctx context.Context, hc *http.Client, pkg string, csv []byte) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: method, Op: opDataSafety, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body:   request{SafetyLabels: string(csv)},
	})
}
