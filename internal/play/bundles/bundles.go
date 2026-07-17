// Package bundles uploads an AAB to a specific Edit via the
// edits.bundles.upload endpoint. The endpoint uses Google's upload sub-host
// and the resumable upload protocol (uploadType=resumable): initiate → chunked
// PUT → resume-from-offset on a transient failure. The resumable state machine
// itself lives in the shared api.ResumableUpload helper; this package only
// opens the AAB and parses the versionCode Google returns.
package bundles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "bundles.upload"

// LocalIOError is returned when the AAB cannot be read from the local
// filesystem (missing path, permission denied, stat failure). It is
// distinct from *api.Error so the exit code maps to client-side
// validation (20 per docs/DESIGN.md §9) rather than transport (50).
type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

// ExitCode satisfies gplay's Coder contract: a missing or unreadable
// AAB is a client-side validation failure, not a transport problem.
func (e *LocalIOError) ExitCode() int { return 20 }

// Upload streams the AAB at aabPath to bundles.upload via the resumable
// protocol and returns the versionCode Google parsed out of the bundle. The
// surface is unchanged (same stdout/JSON, same exit codes): the resource body
// Google returns on the final chunk is the same {"versionCode":N} payload the
// simple-media upload returned. Upstream failures map to *api.Error (DESIGN §9);
// a local file problem maps to *LocalIOError (exit 20).
func Upload(ctx context.Context, hc *http.Client, pkg, editID, aabPath string) (int, error) {
	f, err := os.Open(aabPath)
	if err != nil {
		return 0, &LocalIOError{Path: aabPath, Cause: err}
	}
	defer func() { _ = f.Close() }()

	// Stat the AAB so we can set ContentLength explicitly. Without it,
	// Go's net/http uses chunked Transfer-Encoding (it does NOT special-
	// case *os.File for size inference), which (a) some Google upload
	// front-ends and corporate proxies handle poorly for hundreds-of-MB
	// AABs, and (b) prevents the transport from retrying the request.
	info, err := f.Stat()
	if err != nil {
		return 0, &LocalIOError{Path: aabPath, Cause: err}
	}
	// A directory (or fifo / device) passes os.Open + Stat but cannot be
	// streamed as a request body: the transport would surface it as an
	// *api.Error (exit 50, transport) instead of the client-side
	// *LocalIOError (exit 20) this path is for, and Size() would be wrong
	// for ContentLength. Reject anything but a regular file up front. A
	// symlink to a regular file is fine — os.Open already followed it, so
	// info describes the target, not the link.
	if !info.Mode().IsRegular() {
		return 0, &LocalIOError{Path: aabPath, Cause: fmt.Errorf("not a regular file")}
	}

	u := api.UploadBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/bundles?uploadType=resumable"

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
