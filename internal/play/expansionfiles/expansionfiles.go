// Package expansionfiles performs the hand-rolled HTTP calls for the legacy OBB
// expansion-file surface (edits.expansionfiles; ADR-0007 raw HTTP). An
// ExpansionFile is an Android Publisher Edit artifact keyed by an APK
// apkVersionCode and a type (main|patch) — structurally a sibling of a Mapping
// (CONTEXT.md), uploaded with the same scope and Edit model. The Edit lifecycle
// (begin/commit/discard) is the caller's concern (edits.WithEdit /
// WithReadOnlyEdit); these functions perform a single call inside an already-open
// Edit, given the editID.
//
// Legacy: superseded by Play Asset Delivery for AABs; only APK-based apps use it.
package expansionfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUpload = "expansionfiles.upload"
	opUpdate = "expansionfiles.update"
	opGet    = "expansionfiles.get"
)

// Expansion file types — the expansionFileType path segment. The Discovery
// enum's zero value (expansionFileTypeUnspecified) is never sent.
const (
	TypeMain  = "main"
	TypePatch = "patch"
)

// ExpansionFile is the parsed resource. Exactly one of FileSize (this APK has
// its own uploaded file) or ReferencesVersion (it points at another APK's file)
// is set, per the API schema. FileSize is an int64 carried as a string by the
// API; ReferencesVersion is an int32.
type ExpansionFile struct {
	FileSize          string `json:"fileSize,omitempty"`
	ReferencesVersion int    `json:"referencesVersion,omitempty"`
}

// HasFile reports whether this APK has its own uploaded expansion file (a
// non-zero fileSize), as opposed to referencing another APK's.
func (e ExpansionFile) HasFile() bool {
	return e.FileSize != "" && e.FileSize != "0"
}

// LocalIOError is returned when the .obb cannot be read locally; exit 20
// (client-side validation), distinct from transport. Mirrors bundles/mappings.
type LocalIOError struct {
	Path  string
	Cause error
}

func (e *LocalIOError) Error() string { return fmt.Sprintf("%s: %s: %v", opUpload, e.Path, e.Cause) }
func (e *LocalIOError) Unwrap() error { return e.Cause }
func (e *LocalIOError) ExitCode() int { return 20 }

func fileURL(host, pkg, editID string, versionCode int, fileType string) string {
	return host +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/apks/" + strconv.Itoa(versionCode) +
		"/expansionFiles/" + url.PathEscape(fileType)
}

// Upload streams the .obb at path to expansionfiles.upload, keyed by the APK
// versionCode and type, and returns the verbatim response body
// (ExpansionFilesUploadResponse). Media upload to the upload sub-host.
func Upload(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType, path string) (json.RawMessage, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	u := fileURL(api.UploadBase, pkg, editID, versionCode, fileType) + "?uploadType=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, f)
	if err != nil {
		return nil, &api.Error{Operation: opUpload, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.ContentLength = info.Size()
	req.GetBody = func() (io.ReadCloser, error) { return openRaw(path) }
	req.Header.Set("Content-Type", "application/octet-stream")
	return do(hc, opUpload, pkg, req)
}

// Update declaratively sets the expansion file's referencesVersion (PUT),
// pointing this APK's expansion file at another APK versionCode's already-
// uploaded file. fileSize is output-only, so the request only ever carries
// referencesVersion.
func Update(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType string, referencesVersion int) (json.RawMessage, error) {
	body, err := json.Marshal(ExpansionFile{ReferencesVersion: referencesVersion})
	if err != nil {
		return nil, &api.Error{Operation: opUpdate, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fileURL(api.AndroidPubBase, pkg, editID, versionCode, fileType), bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opUpdate, pkg, req)
}

// Get reads the expansion file configuration (fileSize XOR referencesVersion).
func Get(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType string) (ExpansionFile, json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL(api.AndroidPubBase, pkg, editID, versionCode, fileType), nil)
	if err != nil {
		return ExpansionFile{}, nil, &api.Error{Operation: opGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opGet, pkg, req)
	if err != nil {
		return ExpansionFile{}, nil, err
	}
	var ef ExpansionFile
	if err := json.Unmarshal(raw, &ef); err != nil {
		return ExpansionFile{}, nil, &api.Error{Operation: opGet, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return ef, raw, nil
}

// openRaw opens the file (used by GetBody for retry-safe replay).
func openRaw(path string) (*os.File, error) { return os.Open(path) }

// openRegular opens the file and rejects a non-regular path up front as a
// client-side *LocalIOError (exit 20) — parity with bundles/mappings.Upload —
// so a directory/fifo never surfaces as a transport error (exit 50). It returns
// the os.FileInfo so the caller sets ContentLength without a second Stat.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, &LocalIOError{Path: path, Cause: err}
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, &LocalIOError{Path: path, Cause: err}
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, &LocalIOError{Path: path, Cause: fmt.Errorf("not a regular file")}
	}
	return f, info, nil
}

// do runs req and maps the response to (raw body, *api.Error).
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
