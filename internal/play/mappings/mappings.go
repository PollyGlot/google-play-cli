// Package mappings uploads a ProGuard/R8 deobfuscation file (a Mapping,
// per CONTEXT.md) to a specific Edit via the Android Publisher
// edits.deobfuscationfiles.upload endpoint. Like bundles.upload it uses
// Google's upload sub-host and the resumable protocol (Content-Type:
// application/octet-stream, uploadType=resumable); unlike bundles it is keyed
// by the APK versionCode and the deobfuscation file type.
//
// Despite being functionally coupled to Vitals (it is what makes
// obfuscated crash stack traces readable in Play vitals), a Mapping is
// architecturally a publisher Edit upload — same OAuth scope and Edit
// model as a release upload, NOT the read-only Reporting service — so it
// is surfaced under `releases`, never `vitals` (CONTEXT.md / ADR-0027).
package mappings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const op = "deobfuscationfiles.upload"

// Deobfuscation file types, taken verbatim from the Discovery snapshot's
// deobfuscationFileType enum (docs/discovery/androidpublisher_v3.json) so
// the path segment is never invented. The snapshot's
// `deobfuscationFileTypeUnspecified` is intentionally omitted: gplay never
// sends it.
const (
	// TypeProguard is the ProGuard/R8 mapping.txt — the common case.
	TypeProguard = "proguard"
	// TypeNativeCode is the native debugging symbols file.
	TypeNativeCode = "nativeCode"
)

// Result carries the parsed symbolType plus the raw response body for the
// ADR-0003 JSON pass-through.
type Result struct {
	SymbolType string
	Raw        json.RawMessage
}

// LocalIOError is returned when the mapping cannot be read from the local
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
// mapping is a client-side validation failure, not a transport problem.
func (e *LocalIOError) ExitCode() int { return 20 }

// Upload streams the mapping at path to deobfuscationfiles.upload, keyed
// by versionCode and fileType, and returns the parsed symbolType plus the
// raw body. The Edit lifecycle (begin/commit) is the caller's concern
// (edits.WithEdit); Upload only performs the media POST inside an
// already-open Edit.
func Upload(ctx context.Context, hc *http.Client, pkg, editID string, versionCode int, fileType, path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &LocalIOError{Path: path, Cause: err}
	}
	defer func() { _ = f.Close() }()

	// Stat the file so we can set ContentLength explicitly. Without it,
	// Go's net/http uses chunked Transfer-Encoding (it does NOT special-
	// case *os.File for size inference), which some Google upload
	// front-ends and corporate proxies handle poorly, and which prevents
	// the transport from retrying the request. Native symbol archives can
	// reach the snapshot's 1.6 GiB cap, so this matters here too.
	info, err := f.Stat()
	if err != nil {
		return nil, &LocalIOError{Path: path, Cause: err}
	}
	// A directory (or fifo / device) passes os.Open + Stat but cannot be
	// streamed as a request body: the transport would surface it as an
	// *api.Error (exit 50, transport) instead of the client-side
	// *LocalIOError (exit 20) this path is for, and Size() would be wrong
	// for ContentLength. Reject anything but a regular file up front. A
	// symlink to a regular file is fine — os.Open already followed it, so
	// info describes the target, not the link.
	if !info.Mode().IsRegular() {
		return nil, &LocalIOError{Path: path, Cause: fmt.Errorf("not a regular file")}
	}

	u := api.UploadBase +
		"/applications/" + url.PathEscape(pkg) +
		"/edits/" + url.PathEscape(editID) +
		"/apks/" + strconv.Itoa(versionCode) +
		"/deobfuscationFiles/" + url.PathEscape(fileType) +
		"?uploadType=resumable"

	// *os.File is an io.ReaderAt, giving the resumable helper random access so
	// it can re-send from a server-acknowledged offset after a transient
	// failure without reopening the file. Native symbol archives can reach the
	// snapshot's 1.6 GiB cap, so chunked resume matters here.
	body, status, err := api.ResumableUpload(ctx, hc, op, pkg, u, "application/octet-stream", f, info.Size())
	if err != nil {
		return nil, err
	}
	var parsed struct {
		DeobfuscationFile struct {
			SymbolType string `json:"symbolType"`
		} `json:"deobfuscationFile"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, &api.Error{
			Operation:  op,
			Package:    pkg,
			StatusCode: status,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &Result{SymbolType: parsed.DeobfuscationFile.SymbolType, Raw: body}, nil
}
