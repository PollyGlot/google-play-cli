// Package generatedapks reads (and, via Download, fetches) the APKs Google Play
// generates and signs from an uploaded App Bundle, via the generatedapks.list /
// generatedapks.download endpoints. Unlike releases/tracks these endpoints are
// NOT under the Edit lifecycle — they are direct application-scoped reads at
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

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opList     = "generatedapks.list"
	opDownload = "generatedapks.download"
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
	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) +
		"/generatedApks/" + strconv.FormatInt(versionCode, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ListResponse{}, nil, &api.Error{Operation: opList, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opList, pkg, req)
	if err != nil {
		return ListResponse{}, nil, err
	}
	var lr ListResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return ListResponse{}, nil, &api.Error{Operation: opList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return lr, raw, nil
}

// do runs req and maps the JSON response to (raw body, *api.Error). Used by the
// metadata read (List); Download streams its own (non-JSON) body separately.
func do(hc *http.Client, op, pkg string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
