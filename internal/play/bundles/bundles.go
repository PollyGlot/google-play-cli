// Package bundles uploads an AAB to a specific Edit via the
// edits.bundles.upload endpoint. The endpoint uses Google's
// upload sub-host and the simple-media upload protocol
// (Content-Type: application/octet-stream, uploadType=media).
package bundles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "bundles.upload"

// Upload streams the AAB at aabPath to bundles.upload and returns the
// versionCode Google parsed out of the bundle. Error mapping to gplay
// exit codes will land in Block 4 of the TDD plan; for now only the
// happy path matters.
func Upload(ctx context.Context, hc *http.Client, pkg, editID, aabPath string) (int, error) {
	f, err := os.Open(aabPath)
	if err != nil {
		return 0, &api.Error{
			Operation: op,
			Package:   pkg,
			Message:   fmt.Sprintf("open AAB %s: %v", aabPath, err),
			Cause:     err,
		}
	}
	defer func() { _ = f.Close() }()

	// Stat the AAB so we can set ContentLength explicitly. Without it,
	// Go's net/http uses chunked Transfer-Encoding (it does NOT special-
	// case *os.File for size inference), which (a) some Google upload
	// front-ends and corporate proxies handle poorly for hundreds-of-MB
	// AABs, and (b) prevents the transport from retrying the request.
	info, err := f.Stat()
	if err != nil {
		return 0, &api.Error{
			Operation: op,
			Package:   pkg,
			Message:   fmt.Sprintf("stat AAB %s: %v", aabPath, err),
			Cause:     err,
		}
	}

	u := api.UploadBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/bundles?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, f)
	if err != nil {
		return 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.ContentLength = info.Size()
	// GetBody lets http.Client replay the body across 3xx redirects and
	// across transport-level retries (e.g. an oauth2 token refresh that
	// rebuilds the request). Without it the second attempt would send an
	// empty body and Google would either fail or accept a corrupted upload.
	req.GetBody = func() (io.ReadCloser, error) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return io.NopCloser(f), nil
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		return 0, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    api.APIErrorMessage(body, resp.StatusCode),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed struct {
		VersionCode int `json:"versionCode"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return parsed.VersionCode, nil
}
