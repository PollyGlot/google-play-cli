package artifacts

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opApksList    = "apks.list"
	opBundlesList = "bundles.list"
)

var (
	mApksList    = apiregistry.MustResolve("androidpublisher.edits.apks.list")
	mBundlesList = apiregistry.MustResolve("androidpublisher.edits.bundles.list")
)

type Apk struct {
	VersionCode int64     `json:"versionCode,omitempty"`
	Binary      ApkBinary `json:"binary"`
}

type ApkBinary struct {
	Sha1   string `json:"sha1,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
}

type Bundle struct {
	VersionCode int64  `json:"versionCode,omitempty"`
	Sha1        string `json:"sha1,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

type ApksListResponse struct {
	Apks []Apk `json:"apks,omitempty"`
}

type BundlesListResponse struct {
	Bundles []Bundle `json:"bundles,omitempty"`
}

func ListApks(ctx context.Context, hc *http.Client, pkg, editID string) (ApksListResponse, json.RawMessage, error) {
	var parsed ApksListResponse
	raw, err := get(ctx, hc, mApksList, opApksList, pkg, editID, &parsed)
	return parsed, raw, err
}

func ListBundles(ctx context.Context, hc *http.Client, pkg, editID string) (BundlesListResponse, json.RawMessage, error) {
	var parsed BundlesListResponse
	raw, err := get(ctx, hc, mBundlesList, opBundlesList, pkg, editID, &parsed)
	return parsed, raw, err
}

func get(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, editID string, out any) (json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params: map[string]string{"packageName": pkg, "editId": editID},
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	return raw, nil
}
