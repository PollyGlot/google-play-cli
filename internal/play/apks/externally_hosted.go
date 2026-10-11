package apks

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opExternallyHosted = "apks.addexternallyhosted"

// mExternallyHosted registers an APK served from the organisation's own
// host instead of uploading its bytes (#546). Same Edit, same versionCode
// contract as edits.apks.upload: only the payload differs (a JSON resource,
// not a binary).
var mExternallyHosted = apiregistry.MustResolve("androidpublisher.edits.apks.addexternallyhosted")

// AddExternallyHosted adds the ExternallyHostedApk resource apk to the Edit
// and returns the versionCode Google answers with, plus the verbatim
// response body. apk is sent untouched inside the request envelope: every
// field is Google's to accept or reject (ADR-0003), and its refusal comes back
// as the usual *api.Error. Google restricts the method to Managed Play
// organisations publishing a private app.
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
