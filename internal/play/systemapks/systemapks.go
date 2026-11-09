// Package systemapks creates, reads and downloads System APK variants: the
// APKs Play builds from an uploaded App Bundle for inclusion in a device
// system image (preload / OEM), via the systemapks.variants endpoints. Like
// generatedapks these are NOT under the Edit lifecycle: they are direct
// application-scoped calls at
// /applications/{packageName}/systemApks/{versionCode}/variants (GLOSSARY.md
// "System APK variant"), so the client never opens an Edit. Raw HTTP
// (ADR-0007), never the google-go-sdk.
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

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opCreate   = "systemapks.variants.create"
	opGet      = "systemapks.variants.get"
	opList     = "systemapks.variants.list"
	opDownload = "systemapks.variants.download"
)

// m* are resolved at init, so an unregistered method is a startup panic the
// registry tests catch, never a runtime surprise (#513).
var (
	mCreate   = apiregistry.MustResolve("androidpublisher.systemapks.variants.create")
	mGet      = apiregistry.MustResolve("androidpublisher.systemapks.variants.get")
	mList     = apiregistry.MustResolve("androidpublisher.systemapks.variants.list")
	mDownload = apiregistry.MustResolve("androidpublisher.systemapks.variants.download")
)

// Variant mirrors the API's Variant: the device spec a system APK was built
// for, the build options, and the server-assigned variantId.
type Variant struct {
	VariantID  uint32      `json:"variantId,omitempty"`
	DeviceSpec *DeviceSpec `json:"deviceSpec,omitempty"`
	Options    *Options    `json:"options,omitempty"`
}

// DeviceSpec mirrors DeviceSpec. SupportedAbis is ordered by preference, so
// the order the caller gave is the order sent.
type DeviceSpec struct {
	ScreenDensity    uint32   `json:"screenDensity,omitempty"`
	SupportedAbis    []string `json:"supportedAbis,omitempty"`
	SupportedLocales []string `json:"supportedLocales,omitempty"`
}

// Options mirrors SystemApkOptions.
type Options struct {
	Rotated                     bool `json:"rotated,omitempty"`
	UncompressedDexFiles        bool `json:"uncompressedDexFiles,omitempty"`
	UncompressedNativeLibraries bool `json:"uncompressedNativeLibraries,omitempty"`
}

// ListResponse mirrors SystemApksListResponse. The API does not paginate it.
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

// Create asks Play to build a system APK variant from the bundle uploaded
// under versionCode, for the given device spec and options. It returns the
// created variant (with its variantId) and the verbatim body (ADR-0003).
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

// Get reads one previously created variant.
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

// List reads every variant created for versionCode.
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

// Download streams the raw bytes of one variant to w via alt=media, never
// buffering the whole APK (ADR-0034). Returns the number of bytes written.
func Download(ctx context.Context, hc *http.Client, pkg string, versionCode int64, variantID uint32, w io.Writer) (int64, error) {
	return api.Download(ctx, hc, api.Call{
		Method: mDownload, Op: opDownload, Target: pkg,
		Params: variantParams(pkg, versionCode, variantID),
		Query:  url.Values{"alt": {"media"}},
	}, w)
}
