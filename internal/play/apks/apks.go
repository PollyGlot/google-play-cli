// Package apks uploads a legacy APK to a specific Edit via the
// edits.apks.upload endpoint. It is the legacy sibling of
// internal/play/bundles: the endpoint uses Google's upload sub-host and
// the resumable upload protocol (Content-Type:
// application/octet-stream, uploadType=resumable) and returns the versionCode
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
// versionCode Google parsed out of the APK. The recipe (regular-file
// guard, resumable chunked upload, error mapping) is identical to
// internal/play/bundles.Upload; only the endpoint segment (/apks) and the
// operation tag (apks.upload) differ.
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
		"/apks?uploadType=resumable"

	// *os.File is an io.ReaderAt, giving the resumable helper random access so
	// it can re-send from a server-acknowledged offset after a transient
	// failure without reopening the file.
	body, status, err := api.ResumableUpload(ctx, hc, op, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return 0, err
	}
	var parsed struct {
		VersionCode int `json:"versionCode"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return parsed.VersionCode, nil
}
