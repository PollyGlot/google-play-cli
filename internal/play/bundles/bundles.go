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

// Upload streams the AAB at aabPath to bundles.upload and returns the
// versionCode Google parsed out of the bundle. Error mapping to gplay
// exit codes will land in Block 4 of the TDD plan; for now only the
// happy path matters.
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
	// rebuilds the request). Per net/http's contract, GetBody must return
	// a fresh independent ReadCloser each call — the transport closes
	// Request.Body (the *os.File `f`) after each attempt, so seeking and
	// re-wrapping it would yield reads from a closed handle. Opening a
	// new file handle keeps each retry independent.
	req.GetBody = func() (io.ReadCloser, error) {
		return os.Open(aabPath)
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
