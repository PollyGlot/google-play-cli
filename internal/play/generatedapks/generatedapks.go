// Package generatedapks reads (and, via Download, fetches) the APKs Google Play
// generates and signs from an uploaded App Bundle, via the generatedapks.list /
// generatedapks.download endpoints. Unlike releases/tracks these endpoints are
// NOT under the Edit lifecycle: they are direct application-scoped reads at
// /applications/{packageName}/generatedApks/{versionCode} (CONTEXT.md "Generated
// APK"), so the client must never open a read-only Edit. List returns every
// artifact grouped by signing key, each carrying an opaque downloadId
// (CONTEXT.md "Download ID"). Raw HTTP (ADR-0007), never the google-go-sdk.
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

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opList     = "generatedapks.list"
	opDownload = "generatedapks.download"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). The `:download` custom verb
// is part of the snapshot's flatPath, so it rides the template; only the
// `alt=media` query stays hand-built.
var (
	mList     = apiregistry.MustResolve("androidpublisher.generatedapks.list")
	mDownload = apiregistry.MustResolve("androidpublisher.generatedapks.download")
)

// ListResponse mirrors GeneratedApksListResponse: every generated APK grouped
// by the APK signing key.
type ListResponse struct {
	GeneratedApks []PerSigningKey `json:"generatedApks,omitempty"`
}

// PerSigningKey is one signing-key group (GeneratedApksPerSigningKey): the cert
// hash plus the split / standalone / universal APKs, asset-pack slices, and
// recovery modules signed with it. The Unprotected* lists only appear when the
// app uses automatic protection; their artifacts are downloadable all the same,
// so the flatten surfaces them too.
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

// SplitApk is the download metadata for a split APK (GeneratedSplitApk).
type SplitApk struct {
	DownloadID string `json:"downloadId,omitempty"`
	ModuleName string `json:"moduleName,omitempty"`
	SplitID    string `json:"splitId,omitempty"`
	VariantID  int    `json:"variantId,omitempty"`
}

// StandaloneApk is the download metadata for a standalone APK (GeneratedStandaloneApk).
type StandaloneApk struct {
	DownloadID string `json:"downloadId,omitempty"`
	VariantID  int    `json:"variantId,omitempty"`
}

// UniversalApk is the download metadata for a universal APK (GeneratedUniversalApk).
type UniversalApk struct {
	DownloadID string `json:"downloadId,omitempty"`
}

// AssetPackSlice is the download metadata for an asset-pack slice (GeneratedAssetPackSlice).
type AssetPackSlice struct {
	DownloadID string `json:"downloadId,omitempty"`
	ModuleName string `json:"moduleName,omitempty"`
	SliceID    string `json:"sliceId,omitempty"`
	Version    string `json:"version,omitempty"`
}

// RecoveryApk is the download metadata for an app-recovery module (GeneratedRecoveryApk).
type RecoveryApk struct {
	DownloadID     string `json:"downloadId,omitempty"`
	ModuleName     string `json:"moduleName,omitempty"`
	RecoveryID     string `json:"recoveryId,omitempty"`
	RecoveryStatus string `json:"recoveryStatus,omitempty"`
}

// List enumerates the APKs Play generated and signed from the bundle uploaded
// under versionCode, via generatedapks.list. It returns the parsed response and
// the verbatim body for the ADR-0003 --output json pass-through. No Edit: the
// GET is application-scoped (not under /edits/).
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

// Download streams the raw signed bytes of one generated APK: addressed by its
// opaque downloadID under versionCode: to w, via generatedapks.download with
// alt=media (supportsMediaDownload + useMediaDownloadService). It streams
// rather than buffering (api.Download has no size cap), so a large universal
// APK never lands wholly in memory; it never JSON-unmarshals the success body.
// Returns the number of bytes written. No Edit (the endpoint is
// application-scoped). A non-2xx body is still small JSON, parsed for the error
// envelope.
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
