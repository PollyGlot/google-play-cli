package generatedapks

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
	opList     = "generatedapks.list"
	opDownload = "generatedapks.download"
)

var (
	mList     = apiregistry.MustResolve("androidpublisher.generatedapks.list")
	mDownload = apiregistry.MustResolve("androidpublisher.generatedapks.download")
)

type ListResponse struct {
	GeneratedApks []PerSigningKey `json:"generatedApks,omitempty"`
}

type PerSigningKey struct {
	CertificateSha256Hash              string           `json:"certificateSha256Hash,omitempty"`
	GeneratedSplitApks                 []SplitApk       `json:"generatedSplitApks,omitempty"`
	GeneratedStandaloneApks            []StandaloneApk  `json:"generatedStandaloneApks,omitempty"`
	GeneratedUniversalApk              *UniversalApk    `json:"generatedUniversalApk,omitempty"`
	GeneratedAssetPackSlices           []AssetPackSlice `json:"generatedAssetPackSlices,omitempty"`
	GeneratedRecoveryModules           []RecoveryApk    `json:"generatedRecoveryModules,omitempty"`
	UnprotectedGeneratedSplitApks      []SplitApk       `json:"unprotectedGeneratedSplitApks,omitempty"`
	UnprotectedGeneratedStandaloneApks []StandaloneApk  `json:"unprotectedGeneratedStandaloneApks,omitempty"`
}

type SplitApk struct {
	DownloadID string `json:"downloadId,omitempty"`
	ModuleName string `json:"moduleName,omitempty"`
	SplitID    string `json:"splitId,omitempty"`
	VariantID  int    `json:"variantId,omitempty"`
}

type StandaloneApk struct {
	DownloadID string `json:"downloadId,omitempty"`
	VariantID  int    `json:"variantId,omitempty"`
}

type UniversalApk struct {
	DownloadID string `json:"downloadId,omitempty"`
}

type AssetPackSlice struct {
	DownloadID string `json:"downloadId,omitempty"`
	ModuleName string `json:"moduleName,omitempty"`
	SliceID    string `json:"sliceId,omitempty"`
	Version    string `json:"version,omitempty"`
}

type RecoveryApk struct {
	DownloadID     string `json:"downloadId,omitempty"`
	ModuleName     string `json:"moduleName,omitempty"`
	RecoveryID     string `json:"recoveryId,omitempty"`
	RecoveryStatus string `json:"recoveryStatus,omitempty"`
}

func List(ctx context.Context, hc *http.Client, pkg string, versionCode int64) (ListResponse, json.RawMessage, error) {
	var lr ListResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mList, Op: opList, Target: pkg,
		Params: map[string]string{"packageName": pkg, "versionCode": strconv.FormatInt(versionCode, 10)},
	}, &lr)
	if err != nil {
		return ListResponse{}, nil, err
	}
	return lr, raw, nil
}

func Download(ctx context.Context, hc *http.Client, pkg string, versionCode int64, downloadID string, w io.Writer) (int64, error) {
	return api.Download(ctx, hc, api.Call{
		Method: mDownload, Op: opDownload, Target: pkg,
		Params: map[string]string{
			"packageName": pkg,
			"versionCode": strconv.FormatInt(versionCode, 10),
			"downloadId":  downloadID,
		},
		Query: url.Values{"alt": {"media"}},
	}, w)
}
