// Package sharing uploads an APK or AAB to Google Play Internal App Sharing
// via the internalappsharingartifacts.uploadapk / uploadbundle endpoints and
// returns the resulting shareable artifact. Unlike a release upload these
// endpoints are OUTSIDE the Edit lifecycle (no editId) — they create a private
// InternalAppSharingArtifact whose downloadUrl an authorized tester follows
// into the Play Store, bypassing tracks entirely (CONTEXT.md: Internal App
// Sharing). They use Google's upload sub-host and the simple-media protocol
// (Content-Type: application/octet-stream, uploadType=media), exactly like
// internal/play/bundles, so the proven ContentLength / GetBody recipe applies.
package sharing

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

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opUploadAPK    = "internalappsharingartifacts.uploadapk"
	opUploadBundle = "internalappsharingartifacts.uploadbundle"
)

// Artifact is the parsed InternalAppSharingArtifact: the shareable install
// link plus the artifact's fingerprints. json tags mirror the API verbatim so
// the same struct round-trips the ADR-0003 pass-through. The raw response body
// is returned alongside it for that pass-through (this struct feeds the human
// table/markdown views only).
type Artifact struct {
	// DownloadURL is the private, shareable Play Store install link — the whole
	// point of the command.
	DownloadURL string `json:"downloadUrl,omitempty"`
	// CertificateFingerprint is the SHA-256 of the signing certificate.
	CertificateFingerprint string `json:"certificateFingerprint,omitempty"`
	// SHA256 is the artifact's content hash (matches `sha256sum`).
	SHA256 string `json:"sha256,omitempty"`
}

// LocalIOError is returned when the artifact cannot be read from the local
// filesystem (missing path, permission denied, stat failure, or a non-regular
// file). It is distinct from *api.Error so the exit code maps to client-side
// validation (20 per docs/DESIGN.md §9) rather than transport (50) — mirroring
// internal/play/bundles.LocalIOError.
type LocalIOError struct {
	Op    string
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Path, e.Cause)
}
func (e *LocalIOError) Unwrap() error { return e.Cause }
func (e *LocalIOError) ExitCode() int { return 20 }

// UploadAPK streams the APK at path to internalappsharingartifacts.uploadapk
// and returns the parsed Artifact plus the verbatim response body (for the
// ADR-0003 --output json pass-through).
func UploadAPK(ctx context.Context, hc *http.Client, pkg, path string) (Artifact, json.RawMessage, error) {
	return upload(ctx, hc, opUploadAPK, "apk", pkg, path)
}

// UploadBundle streams the AAB at path to internalappsharingartifacts.uploadbundle.
func UploadBundle(ctx context.Context, hc *http.Client, pkg, path string) (Artifact, json.RawMessage, error) {
	return upload(ctx, hc, opUploadBundle, "bundle", pkg, path)
}

// upload is the shared media-upload body for both endpoints. artifactKind is
// the trailing path segment ("apk"|"bundle"); op tags any *api.Error.
func upload(ctx context.Context, hc *http.Client, op, artifactKind, pkg, path string) (Artifact, json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	// A directory / fifo / device passes Open+Stat but cannot be streamed as a
	// request body, and Size() would be wrong for ContentLength. Reject anything
	// but a regular file up front so it surfaces as the client-side exit-20
	// LocalIOError, not a transport-level *api.Error (exit 50) — parity with
	// internal/play/bundles.Upload.
	if !info.Mode().IsRegular() {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	u := api.UploadBase +
		"/applications/internalappsharing/" + url.PathEscape(pkg) +
		"/artifacts/" + artifactKind + "?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, f)
	if err != nil {
		return Artifact{}, nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	// Explicit ContentLength (Go would otherwise use chunked Transfer-Encoding,
	// which some upload front-ends handle poorly and which blocks transport
	// retries). GetBody re-opens a fresh handle per attempt so a retry/redirect
	// replays the body independently (net/http closes Request.Body each attempt).
	req.ContentLength = info.Size()
	req.GetBody = func() (io.ReadCloser, error) { return os.Open(path) }
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := hc.Do(req)
	if err != nil {
		return Artifact{}, nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return Artifact{}, nil, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		return Artifact{}, nil, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return art, json.RawMessage(raw), nil
}
