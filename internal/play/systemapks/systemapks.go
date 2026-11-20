package systemapks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opCreate   = "systemapks.variants.create"
	opGet      = "systemapks.variants.get"
	opList     = "systemapks.variants.list"
	opDownload = "systemapks.variants.download"
)

var (
	mCreate   = apiregistry.MustResolve("androidpublisher.systemapks.variants.create")
	mGet      = apiregistry.MustResolve("androidpublisher.systemapks.variants.get")
	mList     = apiregistry.MustResolve("androidpublisher.systemapks.variants.list")
	mDownload = apiregistry.MustResolve("androidpublisher.systemapks.variants.download")
)

type Variant struct {
	VariantID  uint32      `json:"variantId,omitempty"`
	DeviceSpec *DeviceSpec `json:"deviceSpec,omitempty"`
	Options    *Options    `json:"options,omitempty"`
}

type DeviceSpec struct {
	ScreenDensity    uint32   `json:"screenDensity,omitempty"`
	SupportedAbis    []string `json:"supportedAbis,omitempty"`
	SupportedLocales []string `json:"supportedLocales,omitempty"`
}

type Options struct {
	Rotated                     bool `json:"rotated,omitempty"`
	UncompressedDexFiles        bool `json:"uncompressedDexFiles,omitempty"`
	UncompressedNativeLibraries bool `json:"uncompressedNativeLibraries,omitempty"`
}

type ListResponse struct {
	Variants []Variant `json:"variants,omitempty"`
}

func params(pkg string, versionCode int64) map[string]string {
	return map[string]string{"packageName": pkg, "versionCode": strconv.FormatInt(versionCode, 10)}
}

func variantParams(pkg string, versionCode int64, variantID uint32) map[string]string {
	p := params(pkg, versionCode)
	p["variantId"] = strconv.FormatUint(uint64(variantID), 10)
	return p
}

func Create(ctx context.Context, hc *http.Client, pkg string, versionCode int64, v Variant) (Variant, json.RawMessage, error) {
	var out Variant
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mCreate, Op: opCreate, Target: pkg,
		Params: params(pkg, versionCode),
		Body:   v,
	}, &out)
	if err != nil {
		return Variant{}, nil, err
	}
	return out, raw, nil
}

func Get(ctx context.Context, hc *http.Client, pkg string, versionCode int64, variantID uint32) (Variant, json.RawMessage, error) {
	var out Variant
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mGet, Op: opGet, Target: pkg,
		Params: variantParams(pkg, versionCode, variantID),
	}, &out)
	if err != nil {
		return Variant{}, nil, err
	}
	return out, raw, nil
}

func List(ctx context.Context, hc *http.Client, pkg string, versionCode int64) (ListResponse, json.RawMessage, error) {
	var lr ListResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mList, Op: opList, Target: pkg,
		Params: params(pkg, versionCode),
	}, &lr)
	if err != nil {
		return ListResponse{}, nil, err
	}
	return lr, raw, nil
}

func Download(ctx context.Context, hc *http.Client, pkg string, versionCode int64, variantID uint32, w io.Writer) (int64, error) {
	return api.Download(ctx, hc, api.Call{
		Method: mDownload, Op: opDownload, Target: pkg,
		Params: variantParams(pkg, versionCode, variantID),
		Query:  url.Values{"alt": {"media"}},
	}, w)
}
