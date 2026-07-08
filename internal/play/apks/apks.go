// Package apks uploads a legacy APK to a specific Edit via the
// edits.apks.upload endpoint. It is the legacy sibling of
// internal/play/bundles: the endpoint uses Google's upload sub-host and
// the simple-media upload protocol (Content-Type:
// application/octet-stream, uploadType=media) and returns the versionCode
// Google parsed out of the APK, after which the release pipeline (track
// assignment, commit, --mapping) is identical (ADR-0036). Google has
// required the AAB for new apps since August 2021, so this surface only
// serves existing apps still distributed as APKs; the API's rejection of
// an APK for an AAB-required app passes through verbatim.
package apks

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

const op = "apks.upload"

// LocalIOError is returned when the APK cannot be read from the local
// filesystem (missing path, permission denied, stat failure, or a
// non-regular file). It is distinct from *api.Error so the exit code maps
// to client-side validation (20 per docs/DESIGN.md §9) rather than
// transport (50) — mirroring internal/play/bundles.LocalIOError.
type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

// ExitCode satisfies gplay's Coder contract: a missing or unreadable
// APK is a client-side validation failure, not a transport problem.
func (e *LocalIOError) ExitCode() int { return 20 }

// Upload streams the APK at apkPath to apks.upload and returns the
// versionCode Google parsed out of the APK. The recipe (explicit
// ContentLength, GetBody re-open, regular-file guard, error mapping) is
// identical to internal/play/bundles.Upload; only the endpoint segment
// (/apks) and the operation tag (apks.upload) differ.
func Upload(ctx context.Context, hc *http.Client, pkg, editID, apkPath string) (int, error) {
	f, err := os.Open(apkPath)
	if err != nil {
		return 0, &LocalIOError{Path: apkPath, Cause: err}
	}
	defer func() { _ = f.Close() }()

	// Stat the APK so we can set ContentLength explicitly. Without it,
	// Go's net/http uses chunked Transfer-Encoding (it does NOT special-
	// case *os.File for size inference), which some Google upload
	// front-ends and corporate proxies handle poorly, and which prevents
	// the transport from retrying the request.
	info, err := f.Stat()
	if err != nil {
		return 0, &LocalIOError{Path: apkPath, Cause: err}
	}
	// A directory (or fifo / device) passes os.Open + Stat but cannot be
	// streamed as a request body: the transport would surface it as an
	// *api.Error (exit 50, transport) instead of the client-side
	// *LocalIOError (exit 20) this path is for, and Size() would be wrong
	// for ContentLength. Reject anything but a regular file up front. A
	// symlink to a regular file is fine — os.Open already followed it, so
	// info describes the target, not the link.
	if !info.Mode().IsRegular() {
		return 0, &LocalIOError{Path: apkPath, Cause: fmt.Errorf("not a regular file")}
	}

	u := api.UploadBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/apks?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, f)
	if err != nil {
		return 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.ContentLength = info.Size()
	// GetBody lets http.Client replay the body across 3xx redirects and
	// across transport-level retries (e.g. an oauth2 token refresh that
	// rebuilds the request). Per net/http's contract, GetBody must return
	// a fresh independent ReadCloser each call — the transport closes
	// Request.Body (the *os.File `f`) after each attempt, so opening a new
	// file handle keeps each retry independent.
	req.GetBody = func() (io.ReadCloser, error) {
		return os.Open(apkPath)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return 0, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
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
