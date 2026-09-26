// Package sharing uploads an APK or AAB to Google Play Internal App Sharing
// via the internalappsharingartifacts.uploadapk / uploadbundle endpoints and
// returns the resulting shareable artifact. Unlike a release upload these
// endpoints are OUTSIDE the Edit lifecycle (no editId): they create a private
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
	"os"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Operation names for *api.Error tagging, matching the REST reference.
const (
	opUploadAPK    = "internalappsharingartifacts.uploadapk"
	opUploadBundle = "internalappsharingartifacts.uploadbundle"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). Both artifacts go to the media
// endpoint (UploadURL), a genuinely different host path than the data plane.
var (
	mUploadAPK    = apiregistry.MustResolve("androidpublisher.internalappsharingartifacts.uploadapk")
	mUploadBundle = apiregistry.MustResolve("androidpublisher.internalappsharingartifacts.uploadbundle")
)

// Artifact is the parsed InternalAppSharingArtifact: the shareable install
// link plus the artifact's fingerprints. json tags mirror the API verbatim so
// the same struct round-trips the ADR-0003 pass-through. The raw response body
// is returned alongside it for that pass-through (this struct feeds the human
// table/markdown views only).
type Artifact struct {
	// DownloadURL is the private, shareable Play Store install link: the whole
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
// validation (20 per docs/DESIGN.md §9) rather than transport (50): mirroring
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
	return upload(ctx, hc, mUploadAPK, opUploadAPK, pkg, path)
}

// UploadBundle streams the AAB at path to internalappsharingartifacts.uploadbundle.
func UploadBundle(ctx context.Context, hc *http.Client, pkg, path string) (Artifact, json.RawMessage, error) {
	return upload(ctx, hc, mUploadBundle, opUploadBundle, pkg, path)
}

// upload is the shared media-upload body for both endpoints. m carries the
// verb and the media-upload URL (the two methods differ only by that URL); op
// tags any *api.Error.
func upload(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, path string) (Artifact, json.RawMessage, error) {
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
	// LocalIOError, not a transport-level *api.Error (exit 50): parity with
	// internal/play/bundles.Upload.
	if !info.Mode().IsRegular() {
		return Artifact{}, nil, &LocalIOError{Op: op, Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	// A Stream with an explicit Size: Go would otherwise use chunked
	// Transfer-Encoding, which some upload front-ends handle poorly and which
	// blocks transport retries; Open runs once per attempt, so a retry or a
	// redirect replays the body from a fresh handle (net/http closes
	// Request.Body each attempt).
	raw, err := api.Do(ctx, hc, api.Call{
		Method: m, Op: op, Target: pkg,
		Params:      map[string]string{"packageName": pkg},
		Media:       true,
		Body:        &api.Stream{Open: func() (io.ReadCloser, error) { return os.Open(path) }, Size: info.Size()},
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return Artifact{}, nil, err
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		// A 2xx that does not decode is the API misbehaving, not the network:
		// it keeps the status tag (exit 30) this module always gave it.
		return Artifact{}, nil, &api.Error{Operation: op, Package: pkg, StatusCode: http.StatusOK, Message: "decode response: " + err.Error(), Cause: err}
	}
	return art, raw, nil
}
