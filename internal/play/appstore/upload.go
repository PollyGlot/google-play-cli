package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Operation tags for the three media-upload methods, matching the REST
// reference method ids so an *api.Error names what the caller invoked.
const (
	opUploadAPK    = "appstoreappsreview.uploadApk"
	opUploadImage  = "appstoreappsreview.uploadImage"
	opUploadPolicy = "appstoreappsreview.uploadAppStoreAppPolicyDeclarationFile"
)

// DeclarationFileTypeDocument is the only meaningful value of the required
// `fileType` field on UploadAppStoreAppPolicyDeclarationFileRequest — the
// enum's other member is the UNSPECIFIED zero value.
const DeclarationFileTypeDocument = "DECLARATION_FILE_TYPE_DOCUMENT"

// LocalIOError is returned when the media file cannot be read from the local
// filesystem (missing path, permission denied, stat failure, non-regular
// file). It is distinct from *api.Error so the exit code maps to client-side
// validation (20 per docs/DESIGN.md §9) rather than transport (50) — parity
// with internal/play/apks and internal/play/customapps.
type LocalIOError struct {
	Op    string
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Path, e.Cause)
}

func (e *LocalIOError) Unwrap() error { return e.Cause }

// ExitCode satisfies gplay's Coder contract: an unreadable local file is a
// client-side validation failure, not a transport problem.
func (e *LocalIOError) ExitCode() int { return 20 }

// UploadAPK streams the APK at path to
// `POST upload/androidpublisher/v3/appstore/{appStorePackageName}/apps/{packageName}/apks:upload`
// and returns the tracking id Google assigns it (`apkId`) plus the verbatim
// response body for the ADR-0003 pass-through.
//
// The id is the whole point of the call: `appstore update` references it in
// `activeApks.activeApkSets[].baseApkId` / `.splitApkId[]`, so an operator
// uploads once and cites the id thereafter rather than re-sending the binary.
func UploadAPK(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	raw, err := uploadMedia(ctx, hc, opUploadAPK, storePackage, pkg, "apks", path, "application/octet-stream", nil)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadAPK, pkg, raw, "apkId")
}

// UploadImage streams the image at path to the `images:upload` endpoint and
// returns its tracking id (`imageId`), cited later by `appstore update` as a
// listing's `appIconId` or one of its `screenshotId` entries.
//
// The endpoint declares `image/*` rather than a blanket octet-stream, so the
// content type is sniffed from the file's leading bytes instead of assumed:
// announcing application/octet-stream on an image-only endpoint invites a
// server-side rejection after the whole transfer.
func UploadImage(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	raw, err := uploadMedia(ctx, hc, opUploadImage, storePackage, pkg, "images", path, "", nil)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadImage, pkg, raw, "imageId")
}

// UploadPolicyDeclarationFile streams the supporting document at path to the
// `policyDeclarationFiles:upload` endpoint and returns its tracking id
// (`fileId`), cited later inside an `appstore update` policy response.
//
// Unlike the other two uploads this method carries a request body: `fileType`
// is required. It therefore opens the resumable session with that JSON and
// streams the file in the chunk PUTs — the same shape customApps.create uses
// for its metadata.
func UploadPolicyDeclarationFile(ctx context.Context, hc *http.Client, storePackage, pkg, path string) (string, json.RawMessage, error) {
	initiate, err := json.Marshal(struct {
		FileType string `json:"fileType"`
	}{FileType: DeclarationFileTypeDocument})
	if err != nil {
		return "", nil, &api.Error{Operation: opUploadPolicy, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	raw, err := uploadMedia(ctx, hc, opUploadPolicy, storePackage, pkg, "policyDeclarationFiles", path, "", initiate)
	if err != nil {
		return "", nil, err
	}
	return trackingID(opUploadPolicy, pkg, raw, "fileId")
}

// uploadMedia is the shared recipe behind the three verbs: guard the local
// file, then hand it to the resumable helper (PRD #355) addressed at
// appstore/{store}/apps/{pkg}/{collection}:upload.
//
// contentType empty means "sniff it from the file". initiate non-nil means the
// method takes a request body, which opens the session.
func uploadMedia(ctx context.Context, hc *http.Client, op, storePackage, pkg, collection, path, contentType string, initiate []byte) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	// The resumable helper needs the exact byte count for
	// X-Upload-Content-Length and the chunk Content-Range headers. A directory
	// or fifo passes Open+Stat but cannot be streamed, and its Size() would be
	// meaningless — reject anything but a regular file up front so the failure
	// stays client-side (exit 20) instead of surfacing as transport (exit 50).
	info, err := f.Stat()
	if err != nil {
		return nil, &LocalIOError{Op: op, Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		return nil, &LocalIOError{Op: op, Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	if contentType == "" {
		contentType, err = sniffContentType(f, info.Size())
		if err != nil {
			return nil, &LocalIOError{Op: op, Path: path, Cause: err}
		}
	}

	u := api.UploadBase +
		"/appstore/" + url.PathEscape(storePackage) +
		"/apps/" + url.PathEscape(pkg) +
		"/" + collection + ":upload?uploadType=resumable"

	// *os.File is an io.ReaderAt, giving the resumable helper random access so
	// it can re-send from a server-acknowledged offset after a transient
	// failure without reopening the file.
	if initiate != nil {
		raw, _, err := api.ResumableUploadWithInitiateBody(
			ctx, hc, op, pkg, u, contentType, f, info.Size(),
			initiate, "application/json; charset=UTF-8",
		)
		return raw, err
	}
	raw, _, err := api.ResumableUpload(ctx, hc, op, pkg, u, contentType, f, info.Size())
	return raw, err
}

// sniffContentType reports the media type of f from its leading bytes.
// http.DetectContentType is the right tool here rather than the file
// extension: an operator's screenshot named `icon` with no suffix is still a
// PNG, and the endpoints police the announced type.
func sniffContentType(f *os.File, size int64) (string, error) {
	n := int64(512)
	if size < n {
		n = size
	}
	if n == 0 {
		return "", fmt.Errorf("file is empty")
	}
	head := make([]byte, n)
	// ReadAt keeps the file offset untouched, so the resumable helper still
	// sees the file from byte 0.
	if _, err := f.ReadAt(head, 0); err != nil {
		return "", err
	}
	return http.DetectContentType(head), nil
}

// trackingID pulls the single id field out of an upload response. The id is
// what makes the call worth running, so a response without one is an error
// rather than a silently empty result — the operator would otherwise carry an
// empty string into `appstore update` and get an opaque rejection there.
func trackingID(op, pkg string, raw json.RawMessage, field string) (string, json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", nil, &api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	var id string
	if v, ok := parsed[field]; ok {
		if err := json.Unmarshal(v, &id); err != nil {
			return "", nil, &api.Error{Operation: op, Package: pkg, Message: "decode response: " + field + ": " + err.Error(), Cause: err}
		}
	}
	if id == "" {
		return "", nil, &api.Error{Operation: op, Package: pkg, Message: "upload succeeded but the response carried no " + field + " — the id is required by `gplay appstore update`"}
	}
	return id, raw, nil
}
