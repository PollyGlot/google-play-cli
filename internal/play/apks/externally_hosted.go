package apks

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opExternallyHosted = "apks.addexternallyhosted"

var mExternallyHosted = apiregistry.MustResolve("androidpublisher.edits.apks.addexternallyhosted")

func AddExternallyHosted(ctx context.Context, hc *http.Client, pkg, editID string, apk json.RawMessage) (int, json.RawMessage, error) {
	var parsed struct {
		ExternallyHostedApk struct {
			VersionCode int `json:"versionCode"`
		} `json:"externallyHostedApk"`
	}
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mExternallyHosted,
		Op:     opExternallyHosted,
		Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
		Body: struct {
			ExternallyHostedApk json.RawMessage `json:"externallyHostedApk"`
		}{apk},
	}, &parsed)
	if err != nil {
		return 0, nil, err
	}
	return parsed.ExternallyHostedApk.VersionCode, raw, nil
}
