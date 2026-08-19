package appstore_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/appstore"
)

const uploadSessionURI = "https://androidpublisher.googleapis.com/upload/session/abc123"

// uploadRT scripts the two-leg resumable exchange: the initiate POST answers
// with a session URI, the single chunk PUT answers with the resource body.
type uploadRT struct {
	initURL      string
	initCT       string // X-Upload-Content-Type announced at initiate
	initBody     []byte
	initBodyCT   string // Content-Type of the initiate request itself
	putBody      []byte
	respStatus   int
	respBody     string
	initFailWith int // non-zero => fail the initiate POST with this status
}

func (r *uploadRT) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodPost:
		r.initURL = req.URL.String()
		r.initCT = req.Header.Get("X-Upload-Content-Type")
		r.initBodyCT = req.Header.Get("Content-Type")
		if req.Body != nil {
			r.initBody, _ = io.ReadAll(req.Body)
		}
		if r.initFailWith != 0 {
			return resp(r.initFailWith, `{"error":{"message":"nope"}}`), nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Location": []string{uploadSessionURI}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil

	case http.MethodPut:
		r.putBody, _ = io.ReadAll(req.Body)
		status := r.respStatus
		if status == 0 {
			status = http.StatusOK
		}
		body := r.respBody
		if body == "" {
			body = `{}`
		}
		return resp(status, body), nil
	}
	return nil, http.ErrNotSupported
}

// writeFile drops content at a temp path and returns it.
func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// pngBytes is a minimal PNG header: enough for http.DetectContentType.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

// TestUploadAPK_requestShape pins the wire shape: the upload host (not the
// plain API host), the app-store-keyed path with the hosted package as a path
// segment, resumable upload type, and no Edit.
func TestUploadAPK_requestShape(t *testing.T) {
	rt := &uploadRT{respBody: `{"apkId":"apk-42"}`}
	path := writeFile(t, "app.apk", []byte("PK\x03\x04 pretend apk"))

	id, raw, err := appstore.UploadAPK(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app", path)
	if err != nil {
		t.Fatalf("UploadAPK: %v", err)
	}
	if id != "apk-42" {
		t.Errorf("apkId = %q, want apk-42", id)
	}
	if !strings.Contains(rt.initURL, "/upload/androidpublisher/v3/") {
		t.Errorf("url %q does not target the media upload host", rt.initURL)
	}
	if !strings.Contains(rt.initURL, "/appstore/com.example.store/apps/com.example.app/apks:upload") {
		t.Errorf("url %q is not the appstoreappsreview.uploadApk endpoint", rt.initURL)
	}
	if !strings.Contains(rt.initURL, "uploadType=resumable") {
		t.Errorf("url %q does not request a resumable upload", rt.initURL)
	}
	if strings.Contains(rt.initURL, "/edits/") {
		t.Errorf("url %q must not open an Edit", rt.initURL)
	}
	if rt.initCT != "application/octet-stream" {
		t.Errorf("X-Upload-Content-Type = %q, want application/octet-stream", rt.initCT)
	}
	// uploadApk takes no request body: the initiate POST opens an empty
	// session, the bytes ride the chunk PUT.
	if len(rt.initBody) != 0 {
		t.Errorf("initiate body = %q, want empty (UploadApkRequest has no fields)", rt.initBody)
	}
	if !strings.Contains(string(rt.putBody), "pretend apk") {
		t.Errorf("chunk PUT did not carry the file bytes: %q", rt.putBody)
	}
	// ADR-0003: the raw body is the source of truth for --output json.
	if string(raw) != `{"apkId":"apk-42"}` {
		t.Errorf("raw = %s, want the verbatim response", raw)
	}
}

// TestUploadAPK_pathEscapesBothKeys guards against a store or package name
// with a path-hostile character silently reshaping the URL.
func TestUploadAPK_pathEscapesBothKeys(t *testing.T) {
	rt := &uploadRT{respBody: `{"apkId":"a"}`}
	path := writeFile(t, "app.apk", []byte("bytes"))

	if _, _, err := appstore.UploadAPK(context.Background(), &http.Client{Transport: rt}, "com.example store", "com.example/app", path); err != nil {
		t.Fatalf("UploadAPK: %v", err)
	}
	if !strings.Contains(rt.initURL, "/appstore/com.example%20store/apps/com.example%2Fapp/apks:upload") {
		t.Errorf("url %q does not escape both path keys", rt.initURL)
	}
}

// TestUploadImage_sniffsContentType pins that the image endpoint is told it is
// receiving an image: it declares `image/*`, so announcing octet-stream would
// invite a rejection after the whole transfer.
func TestUploadImage_sniffsContentType(t *testing.T) {
	rt := &uploadRT{respBody: `{"imageId":"img-7"}`}
	path := writeFile(t, "icon", pngBytes) // no extension on purpose

	id, _, err := appstore.UploadImage(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app", path)
	if err != nil {
		t.Fatalf("UploadImage: %v", err)
	}
	if id != "img-7" {
		t.Errorf("imageId = %q, want img-7", id)
	}
	if rt.initCT != "image/png" {
		t.Errorf("X-Upload-Content-Type = %q, want image/png sniffed from the bytes", rt.initCT)
	}
	if !strings.Contains(rt.initURL, "/apps/com.example.app/images:upload") {
		t.Errorf("url %q is not the uploadImage endpoint", rt.initURL)
	}
}

// TestUploadPolicy_sendsRequiredFileType pins the one upload that carries a
// request body: fileType is required, and it opens the resumable session.
func TestUploadPolicy_sendsRequiredFileType(t *testing.T) {
	rt := &uploadRT{respBody: `{"fileId":"file-9"}`}
	path := writeFile(t, "policy.pdf", []byte("%PDF-1.4\n stub"))

	id, _, err := appstore.UploadPolicyDeclarationFile(context.Background(), &http.Client{Transport: rt}, "com.example.store", "com.example.app", path)
	if err != nil {
		t.Fatalf("UploadPolicyDeclarationFile: %v", err)
	}
	if id != "file-9" {
		t.Errorf("fileId = %q, want file-9", id)
	}
	if !strings.Contains(rt.initURL, "/apps/com.example.app/policyDeclarationFiles:upload") {
		t.Errorf("url %q is not the uploadAppStoreAppPolicyDeclarationFile endpoint", rt.initURL)
	}
	var body struct {
		FileType string `json:"fileType"`
	}
	if err := json.Unmarshal(rt.initBody, &body); err != nil {
		t.Fatalf("initiate body %q is not JSON: %v", rt.initBody, err)
	}
	if body.FileType != appstore.DeclarationFileTypeDocument {
		t.Errorf("fileType = %q, want %q", body.FileType, appstore.DeclarationFileTypeDocument)
	}
	if !strings.HasPrefix(rt.initBodyCT, "application/json") {
		t.Errorf("initiate Content-Type = %q, want application/json", rt.initBodyCT)
	}
}

// TestUpload_missingFile_exit20 keeps an unreadable local path client-side
// (exit 20) instead of letting it surface as transport (exit 50).
func TestUpload_missingFile_exit20(t *testing.T) {
	rt := &uploadRT{}
	_, _, err := appstore.UploadAPK(context.Background(), &http.Client{Transport: rt}, "s", "p", filepath.Join(t.TempDir(), "nope.apk"))
	assertExitCode(t, err, 20)
	if rt.initURL != "" {
		t.Errorf("a missing file must fail before any HTTP call, got %q", rt.initURL)
	}
}

// TestUpload_directory_exit20 covers the path that passes Open and Stat but
// cannot be streamed.
func TestUpload_directory_exit20(t *testing.T) {
	_, _, err := appstore.UploadImage(context.Background(), &http.Client{Transport: &uploadRT{}}, "s", "p", t.TempDir())
	assertExitCode(t, err, 20)
}

// TestUpload_emptyFile_exit20 rejects a zero-byte file: there is nothing to
// sniff and nothing worth uploading.
func TestUpload_emptyFile_exit20(t *testing.T) {
	_, _, err := appstore.UploadImage(context.Background(), &http.Client{Transport: &uploadRT{}}, "s", "p", writeFile(t, "empty.png", nil))
	assertExitCode(t, err, 20)
}

// TestUpload_responseWithoutID_errors: the tracking id is the whole product of
// the call. Returning an empty string would push an opaque failure downstream
// into `appstore update`.
func TestUpload_responseWithoutID_errors(t *testing.T) {
	rt := &uploadRT{respBody: `{}`}
	_, _, err := appstore.UploadAPK(context.Background(), &http.Client{Transport: rt}, "s", "p", writeFile(t, "a.apk", []byte("x")))
	if err == nil {
		t.Fatal("want an error when the response carries no apkId, got nil")
	}
	if !strings.Contains(err.Error(), "apkId") {
		t.Errorf("error %q does not name the missing field", err)
	}
}

// TestUpload_403_exit11 pins that upstream failures keep the shared taxonomy.
func TestUpload_403_exit11(t *testing.T) {
	rt := &uploadRT{initFailWith: http.StatusForbidden}
	_, _, err := appstore.UploadAPK(context.Background(), &http.Client{Transport: rt}, "s", "p", writeFile(t, "a.apk", []byte("x")))
	assertExitCode(t, err, 11)
}

// TestUpload_404_exit30 covers the store or hosted app not existing.
func TestUpload_404_exit30(t *testing.T) {
	rt := &uploadRT{initFailWith: http.StatusNotFound}
	_, _, err := appstore.UploadImage(context.Background(), &http.Client{Transport: rt}, "s", "p", writeFile(t, "i.png", pngBytes))
	assertExitCode(t, err, 30)
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with exit %d, got nil", want)
	}
	if got := exit.For(err); got != want {
		t.Errorf("exit = %d, want %d (err: %v)", got, want, err)
	}
}
