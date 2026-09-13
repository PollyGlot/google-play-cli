// Package artifacts reads the APKs and App Bundles attached to an app inside an
// Edit, via edits.apks.list and edits.bundles.list. Both resources are
// Edit-scoped (unlike generatedapks), so the caller owns the Edit: a read-only
// one that is discarded, or the pinned explicit one so artifacts uploaded in
// it and not yet committed are visible. Raw HTTP (ADR-0007), never the
// google-go-sdk; verbatim bodies come back for the ADR-0003 JSON pass-through.
package artifacts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opApksList    = "apks.list"
	opBundlesList = "bundles.list"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of literals kept here (#513).
var (
	mApksList    = apiregistry.MustResolve("androidpublisher.edits.apks.list")
	mBundlesList = apiregistry.MustResolve("androidpublisher.edits.bundles.list")
)

// Apk mirrors the API's Apk resource: the version code plus the binary hashes.
type Apk struct {
	VersionCode int64     `json:"versionCode,omitempty"`
	Binary      ApkBinary `json:"binary"`
}

// ApkBinary mirrors ApkBinary: hex hashes matching sha1sum / sha256sum.
type ApkBinary struct {
	Sha1   string `json:"sha1,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
}

// Bundle mirrors the API's Bundle resource. Unlike Apk, the hashes sit at the
// top level rather than under a binary sub-object.
type Bundle struct {
	VersionCode int64  `json:"versionCode,omitempty"`
	Sha1        string `json:"sha1,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

// ApksListResponse mirrors ApksListResponse.
type ApksListResponse struct {
	Apks []Apk `json:"apks,omitempty"`
}

// BundlesListResponse mirrors BundlesListResponse.
type BundlesListResponse struct {
	Bundles []Bundle `json:"bundles,omitempty"`
}

// ListApks enumerates the APKs attached to the Edit via edits.apks.list. It
// returns the parsed response and the verbatim body.
func ListApks(ctx context.Context, hc *http.Client, pkg, editID string) (ApksListResponse, json.RawMessage, error) {
	var parsed ApksListResponse
	raw, err := get(ctx, hc, mApksList, opApksList, pkg, editID, &parsed)
	return parsed, raw, err
}

// ListBundles enumerates the App Bundles attached to the Edit via
// edits.bundles.list. It returns the parsed response and the verbatim body.
func ListBundles(ctx context.Context, hc *http.Client, pkg, editID string) (BundlesListResponse, json.RawMessage, error) {
	var parsed BundlesListResponse
	raw, err := get(ctx, hc, mBundlesList, opBundlesList, pkg, editID, &parsed)
	return parsed, raw, err
}

// get issues the Edit-scoped GET for m and decodes the 2xx body into out. The
// two list methods differ only by resource segment and response shape, so the
// transport recipe (error envelope on non-2xx, bounded body reads) lives once.
func get(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, editID string, out any) (json.RawMessage, error) {
	u, err := m.URL(map[string]string{"packageName": pkg, "editId": editID})
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, m.Verb, u, http.NoBody)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + err.Error(), Cause: err}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error(), Cause: err}
	}
	return json.RawMessage(raw), nil
}
