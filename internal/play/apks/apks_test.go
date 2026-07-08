// Package apks_test exercises the play-layer legacy-APK upload against a
// fake transport. Upload wraps the Android Publisher edits.apks.upload
// endpoint: a simple-media POST of the APK into an already-open Edit,
// mirroring internal/play/bundles.
package apks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/apks"
)

// rt is a minimal RoundTripper that records the request line + query and
// returns a canned apks.upload response. A test that expects no HTTP
// asserts gotMethod stayed "".
type rt struct {
	status int
	body   string

	gotMethod string
	gotPath   string
	gotQuery  string
	gotCType  string
	gotLen    int64
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	r.gotMethod = req.Method
	r.gotPath = req.URL.Path
	r.gotQuery = req.URL.RawQuery
	r.gotCType = req.Header.Get("Content-Type")
	r.gotLen = req.ContentLength
	status := r.status
	if status == 0 {
		status = 200
	}
	body := r.body
	if body == "" {
		body = `{"versionCode":7}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func writeFakeAPK(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(p, []byte("fake-apk-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

// TestUpload_happyPath_hitsApksMediaPath_returnsVersionCode asserts the
// wire path is /apks with uploadType=media, the simple-media headers are
// set, and the parsed versionCode comes back.
func TestUpload_happyPath_hitsApksMediaPath_returnsVersionCode(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{body: `{"versionCode":142}`}
	hc := &http.Client{Transport: transport}

	vc, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if vc != 142 {
		t.Errorf("versionCode = %d, want 142", vc)
	}
	wantPath := "/upload/androidpublisher/v3/applications/com.example.app/edits/edit-1/apks"
	if transport.gotPath != wantPath {
		t.Errorf("path = %q, want %q", transport.gotPath, wantPath)
	}
	if transport.gotQuery != "uploadType=media" {
		t.Errorf("query = %q, want uploadType=media", transport.gotQuery)
	}
	if transport.gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", transport.gotMethod)
	}
	if transport.gotCType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", transport.gotCType)
	}
	// Explicit ContentLength (not chunked) — parity with bundles.Upload.
	if transport.gotLen != int64(len("fake-apk-content")) {
		t.Errorf("ContentLength = %d, want %d", transport.gotLen, len("fake-apk-content"))
	}
}

// TestUpload_400_mapsToExit20 asserts a 400 on apks.upload (malformed APK
// / AAB-required app) surfaces as client-side validation (exit 20), the
// same operation-aware nuance bundles.upload gets in the API error mapper.
func TestUpload_400_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{status: 400, body: `{"error":{"code":400,"message":"APK not allowed; app requires an App Bundle."}}`}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 400 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

// TestUpload_404_mapsToExit20 mirrors the 400 case: a 404 on apks.upload
// is also treated as malformed-artifact client-side validation (exit 20).
func TestUpload_404_mapsToExit20(t *testing.T) {
	apk := writeFakeAPK(t)
	transport := &rt{status: 404, body: `{"error":{"code":404,"message":"Not found."}}`}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", apk)
	if err == nil {
		t.Fatal("Upload accepted a 404 response")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
}

// TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP asserts a
// non-regular path (a directory) is rejected as a client-side validation
// error (exit 20) BEFORE any HTTP — os.Open+Stat both succeed on a
// directory, so without an explicit regular-file guard it would only fail
// later as a transport error (exit 50). Mirrors bundles.Upload's guard.
func TestUpload_directoryPath_returnsLocalIOError_exit20_noHTTP(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	transport := &rt{}
	hc := &http.Client{Transport: transport}

	_, err := apks.Upload(context.Background(), hc, "com.example.app", "edit-1", dir)
	if err == nil {
		t.Fatal("Upload accepted a directory as an APK path")
	}
	if got := exit.For(err); got != 20 {
		t.Errorf("exit.For(err) = %d, want 20; err=%v", got, err)
	}
	var ioErr *apks.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Errorf("error %v is not *apks.LocalIOError", err)
	}
	if transport.gotMethod != "" {
		t.Errorf("uploader hit the network for a directory path: %s %s", transport.gotMethod, transport.gotPath)
	}
}
